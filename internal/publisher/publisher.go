// Package publisher turns a poll result into retained MQTT messages: the Home
// Assistant discovery configs, one JSON state document per device, and the
// availability topics that make an untracked or unheld position grey out on its
// own. See docs/specs/03-mqtt-ha-discovery.md.
package publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/gsdevme/trading212-mqtt/internal/homeassistant"
	"github.com/gsdevme/trading212-mqtt/internal/trading212"
)

// Publisher is the MQTT transport. Discovery, state and availability are always
// published retained, and the transport uses QoS 1. Implementations: the
// autopaho client in production and a recording fake in tests.
type Publisher interface {
	Publish(ctx context.Context, topic string, payload []byte, retain bool) error
}

const (
	payloadOnline  = "online"
	payloadOffline = "offline"
)

// discoveryEntry is what the Service remembers about one tracked position: the
// original ticker string (used to build discovery payloads and topics — the
// device name and topic helpers all derive from it) and the metadata
// fingerprint its discovery was last built from.
//
// discoveryEntry is stored keyed by trading212.Slug(ticker) rather than by the
// raw ticker — see the discovered field's doc for why.
type discoveryEntry struct {
	ticker      string
	fingerprint string
}

// Service publishes discovery and state for one account and its tracked
// positions.
//
// It owns the tracked-position lifecycle: which tickers have discovery
// published, the instrument metadata that discovery was built from, and whether
// each is currently held. That state is why a sold-out position can grey out
// without its discovery configs being deleted and re-added.
type Service struct {
	pub Publisher
	cfg homeassistant.Config
	wl  trading212.Whitelist

	mu sync.Mutex
	// discovered maps trading212.Slug(ticker) to what its discovery was built
	// from. It is keyed by slug, not by the raw ticker, because a whitelist
	// entry ("aapl_us_eq") and the API's ticker for the same holding
	// ("AAPL_US_EQ") must resolve to the same tracked position — they publish
	// to the same MQTT topics. Keying by raw ticker would create two entries
	// for one topic set: the end-of-poll "not held" sweep would then publish a
	// retained offline to a topic the very same poll had just marked online,
	// which is visible entity flapping in Home Assistant.
	discovered map[string]discoveryEntry
	// online maps trading212.Slug(ticker) to its last published availability,
	// for the same reason discovered is keyed by slug.
	online map[string]bool
}

// New builds a Service.
func New(pub Publisher, cfg homeassistant.Config, wl trading212.Whitelist) *Service {
	return &Service{
		pub:        pub,
		cfg:        cfg,
		wl:         wl,
		discovered: map[string]discoveryEntry{},
		online:     map[string]bool{},
	}
}

// AvailabilityTopic exposes the service-level topic, used as the MQTT Last Will.
func (s *Service) AvailabilityTopic() string { return s.cfg.AvailabilityTopic() }

// PublishDiscovery publishes the account device's discovery configs, plus
// placeholder configs for every explicitly whitelisted ticker.
//
// A placeholder names the device after the raw ticker and assumes the account
// currency, because the real instrument metadata only arrives with the first
// poll that sees the position. Publishing it up front means a whitelisted
// holding appears in Home Assistant immediately — greyed out — rather than
// silently missing until it is next held.
func (s *Service) PublishDiscovery(ctx context.Context) error {
	msgs, err := homeassistant.BuildAccountDiscovery(s.cfg)
	if err != nil {
		return err
	}
	if err := s.publishAll(ctx, msgs); err != nil {
		return err
	}

	for _, ticker := range s.wl.Static() {
		if err := s.ensurePositionDiscovery(ctx, ticker, "", ""); err != nil {
			return err
		}
		if err := s.setPositionAvailability(ctx, ticker, false); err != nil {
			return err
		}
	}
	return nil
}

// PublishAvailability publishes the service-level online/offline state.
func (s *Service) PublishAvailability(ctx context.Context, online bool) error {
	return s.pub.Publish(ctx, s.cfg.AvailabilityTopic(), []byte(availabilityPayload(online)), true)
}

// PublishOffline marks the service and every tracked position offline. Used on
// graceful shutdown, where the Last Will alone would not fire.
//
// It iterates discovered, not online, so a placeholder that was never held is
// still explicitly marked offline on shutdown.
func (s *Service) PublishOffline(ctx context.Context) error {
	for _, t := range s.trackedTickers() {
		if err := s.setPositionAvailability(ctx, t, false); err != nil {
			return err
		}
	}
	return s.PublishAvailability(ctx, false)
}

// PublishSnapshot publishes one poll's worth of state: the account document,
// then each tracked position's document and availability.
//
// Positions are filtered client-side against the whitelist. A tracked ticker
// that is not in the snapshot is marked offline but keeps its discovery, so
// selling out and buying back in needs no discovery churn.
func (s *Service) PublishSnapshot(ctx context.Context, snap trading212.Snapshot) error {
	body, err := json.Marshal(snap.Account)
	if err != nil {
		return fmt.Errorf("marshal account state: %w", err)
	}
	if err := s.pub.Publish(ctx, s.cfg.StateTopic(), body, true); err != nil {
		return fmt.Errorf("publish account state: %w", err)
	}

	held := map[string]bool{}
	for _, p := range snap.Positions {
		if !s.wl.Includes(p.Ticker) {
			continue
		}
		held[trading212.Slug(p.Ticker)] = true
		if err := s.publishPosition(ctx, p); err != nil {
			return err
		}
	}

	// Anything tracked but absent from this poll is no longer held.
	for _, ticker := range s.trackedTickers() {
		if held[trading212.Slug(ticker)] {
			continue
		}
		if err := s.setPositionAvailability(ctx, ticker, false); err != nil {
			return err
		}
	}
	return nil
}

// publishPosition publishes one position's discovery (if needed), state and
// availability.
func (s *Service) publishPosition(ctx context.Context, p trading212.Position) error {
	if err := s.ensurePositionDiscovery(ctx, p.Ticker, p.Name, p.InstrumentCurrency); err != nil {
		return err
	}
	body, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal position state %s: %w", p.Ticker, err)
	}
	if err := s.pub.Publish(ctx, s.cfg.PositionStateTopic(p.Ticker), body, true); err != nil {
		return fmt.Errorf("publish position state %s: %w", p.Ticker, err)
	}
	return s.setPositionAvailability(ctx, p.Ticker, true)
}

// ensurePositionDiscovery publishes a position's discovery configs the first
// time it is seen, and again whenever the metadata that shapes them changes —
// which is how a placeholder built from the raw ticker is refined into the real
// instrument name and currency.
func (s *Service) ensurePositionDiscovery(ctx context.Context, ticker, name, instrumentCurrency string) error {
	slug := trading212.Slug(ticker)
	fingerprint := name + "|" + instrumentCurrency

	s.mu.Lock()
	entry, seen := s.discovered[slug]
	s.mu.Unlock()
	if seen && entry.fingerprint == fingerprint {
		return nil
	}

	msgs, err := homeassistant.BuildPositionDiscovery(s.cfg, ticker, name, instrumentCurrency)
	if err != nil {
		return err
	}
	if err := s.publishAll(ctx, msgs); err != nil {
		return err
	}

	s.mu.Lock()
	s.discovered[slug] = discoveryEntry{ticker: ticker, fingerprint: fingerprint}
	s.mu.Unlock()
	return nil
}

// setPositionAvailability publishes a position's availability, skipping the
// publish when it has not changed.
func (s *Service) setPositionAvailability(ctx context.Context, ticker string, online bool) error {
	slug := trading212.Slug(ticker)

	s.mu.Lock()
	prev, seen := s.online[slug]
	unchanged := seen && prev == online
	s.mu.Unlock()
	if unchanged {
		return nil
	}

	topic := s.cfg.PositionAvailabilityTopic(ticker)
	if err := s.pub.Publish(ctx, topic, []byte(availabilityPayload(online)), true); err != nil {
		return fmt.Errorf("publish availability %s: %w", ticker, err)
	}

	s.mu.Lock()
	s.online[slug] = online
	s.mu.Unlock()
	return nil
}

// trackedTickers returns the original ticker string for every position that
// has discovery published, recovered from discovered's slug-keyed entries so
// callers can rebuild topics and device names correctly.
func (s *Service) trackedTickers() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.discovered))
	for _, entry := range s.discovered {
		out = append(out, entry.ticker)
	}
	return out
}

func (s *Service) publishAll(ctx context.Context, msgs []homeassistant.Message) error {
	for _, m := range msgs {
		if err := s.pub.Publish(ctx, m.Topic, m.Payload, true); err != nil {
			return fmt.Errorf("publish discovery %s: %w", m.Topic, err)
		}
	}
	return nil
}

func availabilityPayload(online bool) string {
	if online {
		return payloadOnline
	}
	return payloadOffline
}
