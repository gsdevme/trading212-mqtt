package trading212

import "time"

// Endpoint paths, relative to the configured base URL. These two are the entire
// API surface this service is permitted to touch — see REQ-T2-05.
const (
	pathAccountSummary = "/equity/account/summary"
	pathPositions      = "/equity/positions"
)

// Published rate limits, from docs/specs/01-trading212-api.md. Limits are
// enforced per account, so these are shared with anything else using the same
// credentials.
const (
	limitAccountSummary = 5 * time.Second
	limitPositions      = 1 * time.Second
)

// Rate-limit response headers.
const (
	headerRateLimitRemaining = "X-Ratelimit-Remaining"
	headerRateLimitReset     = "X-Ratelimit-Reset"
)
