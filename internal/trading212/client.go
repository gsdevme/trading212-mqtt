package trading212

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ErrUnauthorized is returned for 401 and 403 responses: the credentials are
// missing, wrong, or not permitted for this account.
var ErrUnauthorized = errors.New("trading212: unauthorized")

// RateLimitError is returned for 429 responses. ResetAt is taken from the
// x-ratelimit-reset header when present.
type RateLimitError struct {
	ResetAt time.Time
}

func (e *RateLimitError) Error() string {
	if e.ResetAt.IsZero() {
		return "trading212: rate limited"
	}
	return fmt.Sprintf("trading212: rate limited until %s", e.ResetAt.Format(time.RFC3339))
}

// maxBodyBytes caps how much of a response is read. The largest realistic
// response is a positions list; this leaves generous headroom while bounding
// memory on a malformed or hostile response.
const maxBodyBytes = 8 << 20 // 8 MiB

// Config configures the client.
type Config struct {
	// BaseURL is the fully-resolved API root, including /api/v0.
	BaseURL string
	// APIKey is the API key. APISecret may be empty, which selects the legacy
	// bare-key Authorization scheme.
	APIKey    string
	APISecret string

	HTTPClient *http.Client
	Logger     *slog.Logger
}

// Client is a read-only Trading 212 Public API client. It exposes exactly the
// two GET endpoints this service is permitted to call.
type Client struct {
	baseURL string
	auth    string
	http    *http.Client
	logger  *slog.Logger

	summaryLimiter   *Limiter
	positionsLimiter *Limiter

	now func() time.Time
}

// New builds a Client. It validates that the base URL and key are present so a
// misconfiguration fails at startup rather than on the first poll.
func New(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("trading212: BaseURL is required")
	}
	if cfg.APIKey == "" {
		return nil, errors.New("trading212: APIKey is required")
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{
		baseURL:          strings.TrimRight(cfg.BaseURL, "/"),
		auth:             authHeader(cfg.APIKey, cfg.APISecret),
		http:             hc,
		logger:           logger,
		summaryLimiter:   NewLimiter(limitAccountSummary),
		positionsLimiter: NewLimiter(limitPositions),
		now:              time.Now,
	}, nil
}

// authHeader builds the Authorization value. With a secret it is HTTP Basic;
// without one it falls back to the legacy scheme, which sends the bare key.
func authHeader(key, secret string) string {
	if secret == "" {
		return key
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(key+":"+secret))
}

// AccountSummary fetches the account totals, id and primary currency.
func (c *Client) AccountSummary(ctx context.Context) (AccountSummary, error) {
	body, err := c.get(ctx, pathAccountSummary, c.summaryLimiter)
	if err != nil {
		return AccountSummary{}, fmt.Errorf("account summary: %w", err)
	}
	return parseAccountSummary(body, c.now())
}

// Positions fetches every open position. No ticker filter is sent: one
// unfiltered call is cheaper than one call per tracked ticker, and filtering
// happens client-side.
func (c *Client) Positions(ctx context.Context, accountCurrency string) ([]Position, error) {
	body, err := c.get(ctx, pathPositions, c.positionsLimiter)
	if err != nil {
		return nil, fmt.Errorf("positions: %w", err)
	}
	return parsePositions(body, accountCurrency)
}

// Get fetches a path and returns the raw body, using the same auth and pacing as
// the typed calls. It backs the dump diagnostic; the result goes to stdout and
// is never written to disk.
func (c *Client) Get(ctx context.Context, path string) ([]byte, error) {
	return c.get(ctx, path, c.positionsLimiter)
}

// get performs one paced, authenticated GET and maps the response status onto
// this package's error vocabulary.
func (c *Client) get(ctx context.Context, path string, lim *Limiter) ([]byte, error) {
	if err := lim.Wait(ctx); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	lim.Observe(resp.Header)

	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("GET %s: %w (status %d)", path, ErrUnauthorized, resp.StatusCode)
	case resp.StatusCode == http.StatusTooManyRequests:
		rle := &RateLimitError{ResetAt: resetFromHeader(resp.Header)}
		if !rle.ResetAt.IsZero() {
			lim.HoldUntil(rle.ResetAt)
		}
		return nil, fmt.Errorf("GET %s: %w", path, rle)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return nil, fmt.Errorf("GET %s: unexpected status %d", path, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("read %s response: %w", path, err)
	}
	return body, nil
}

func resetFromHeader(h http.Header) time.Time {
	ts, err := strconv.ParseInt(h.Get(headerRateLimitReset), 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(ts, 0)
}
