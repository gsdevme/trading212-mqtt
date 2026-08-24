// Package mqtt provides an autopaho-backed MQTT 5 client satisfying
// publisher.Publisher. It configures the Last Will (a retained "offline" on the
// availability topic) and reconnects automatically.
package mqtt

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"sync"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
)

// Options configure the MQTT connection.
type Options struct {
	BrokerURL string
	Username  string
	Password  string
	ClientID  string
	// AvailabilityTopic is registered as the Last Will topic, carrying a
	// retained "offline" if the connection drops unexpectedly.
	AvailabilityTopic string
	Logger            *slog.Logger

	// OnConnectionUp is invoked in a goroutine each time the connection is
	// (re)established, so callers can republish availability and discovery. It
	// may block.
	OnConnectionUp func(ctx context.Context)
}

// Client wraps an autopaho ConnectionManager and implements publisher.Publisher.
type Client struct {
	cm     *autopaho.ConnectionManager
	logger *slog.Logger

	mu   sync.Mutex
	onUp func(ctx context.Context)
}

// Connect establishes the MQTT connection (with the Last Will) and waits for it
// to come up.
func Connect(ctx context.Context, opts Options) (*Client, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	u, err := url.Parse(opts.BrokerURL)
	if err != nil {
		return nil, fmt.Errorf("parse MQTT broker URL: %w", err)
	}

	c := &Client{logger: opts.Logger, onUp: opts.OnConnectionUp}

	cfg := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{u},
		KeepAlive:                     20,
		CleanStartOnInitialConnection: false,
		ConnectUsername:               opts.Username,
		ConnectPassword:               []byte(opts.Password),
		OnConnectError: func(err error) {
			opts.Logger.Warn("mqtt connection attempt failed", "err", err)
		},
		OnConnectionUp: func(cm *autopaho.ConnectionManager, _ *paho.Connack) {
			opts.Logger.Info("mqtt connected")
			// autopaho starts its dial goroutine immediately and gives no
			// ordering guarantee against NewConnection's return, so this
			// closure — not the assignment after NewConnection below — is what
			// makes c.cm safe to read from Publish/Disconnect the first time
			// this fires. Assign it under the same mutex that guards onUp,
			// before firing the user callback.
			c.mu.Lock()
			c.cm = cm
			c.mu.Unlock()
			c.fireOnUp(ctx)
		},
		ClientConfig: paho.ClientConfig{ClientID: opts.ClientID},
	}
	if opts.AvailabilityTopic != "" {
		//nolint:staticcheck // SA1019: the helper still encodes the WillProperties defaults this LWT needs.
		cfg.SetWillMessage(opts.AvailabilityTopic, []byte("offline"), 1, true)
	}

	cm, err := autopaho.NewConnection(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("mqtt new connection: %w", err)
	}
	// Harmless alongside the OnConnectionUp assignment above, and covers the
	// no-callback path.
	c.mu.Lock()
	c.cm = cm
	c.mu.Unlock()
	if err := cm.AwaitConnection(ctx); err != nil {
		return nil, fmt.Errorf("mqtt await connection: %w", err)
	}
	return c, nil
}

// SetOnConnectionUp registers a callback invoked in a goroutine each time the
// connection is (re)established — used to republish availability and discovery
// after a broker restart. Safe to call after Connect.
func (c *Client) SetOnConnectionUp(f func(ctx context.Context)) {
	c.mu.Lock()
	c.onUp = f
	c.mu.Unlock()
}

func (c *Client) fireOnUp(ctx context.Context) {
	c.mu.Lock()
	f := c.onUp
	c.mu.Unlock()
	if f != nil {
		go f(ctx) // must not block the paho callback
	}
}

// Publish sends a message at QoS 1 with the given retain flag.
func (c *Client) Publish(ctx context.Context, topic string, payload []byte, retain bool) error {
	c.mu.Lock()
	cm := c.cm
	c.mu.Unlock()

	_, err := cm.Publish(ctx, &paho.Publish{
		QoS:     1,
		Topic:   topic,
		Payload: payload,
		Retain:  retain,
	})
	if err != nil {
		return fmt.Errorf("mqtt publish %s: %w", topic, err)
	}
	return nil
}

// Disconnect closes the connection cleanly, which suppresses the Last Will.
func (c *Client) Disconnect(ctx context.Context) error {
	c.mu.Lock()
	cm := c.cm
	c.mu.Unlock()

	return cm.Disconnect(ctx)
}
