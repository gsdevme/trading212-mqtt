// Package publisher turns a poll result into retained MQTT messages: the Home
// Assistant discovery configs, one JSON state document per device, and the
// availability topics that make an untracked or unheld position grey out on its
// own. See docs/specs/03-mqtt-ha-discovery.md.
package publisher

import (
	"context"
	"encoding/json"
	"errors"
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
// device name and topic helpers all derive from it) plus the instrument
// metadata its discovery was last built from.
//
// name and instrumentCurrency are stored as distinct fields, not as a joined
// fingerprint string, because Republish rebuilds discovery from them: splitting
// a joined value back apart would corrupt any instrument whose name contains
// the separator.
//
// discoveryEntry is stored keyed by trading212.Slug(ticker) rather than by the
// raw ticker — see the discovered field's doc for why.
type discoveryEntry struct {
	ticker             string
	name               string
	instrumentCurrency string
}

// matches reports whether discovery built from this metadata would be identical
// to what is already published, which is what lets ensurePositionDiscovery skip
// a redundant publish.
func (e discoveryEntry) matches(name, instrumentCurrency string) bool {
	return e.name == name && e.instrumentCurrency == instrumentCurrency
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

	// publishMu serialises the five public entry points (PublishDiscovery,
	// Republish, PublishSnapshot, PublishAvailability, PublishOffline) against
	// each other.
	// It is held for the whole body of each, distinct from mu below, so that an
	// MQTT reconnect's PublishAvailability/PublishDiscovery — fired from
	// internal/mqtt's `go f(ctx)` callback — cannot interleave with the
	// scheduler's concurrent PublishSnapshot. Without it, a reconnect landing
	// mid-poll can race the placeholder discovery path (name and instrument
	// currency both empty) against the refined path ("Apple Inc."/"USD") and
	// leave the placeholder's write as the last one in, stranding a position's
	// retained discovery on the raw ticker and account currency until the next
	// poll.
	//
	// publishMu is never held across the network publishes done under mu below
	// — those two mutexes have distinct jobs and holding the map mutex across a
	// publish would serialise every publish behind it.
	publishMu sync.Mutex
	// closed is set by PublishOffline, under publishMu, marking the service as
	// shut down: PublishDiscovery, Republish, PublishSnapshot and
	// PublishAvailability all check it immediately after acquiring publishMu
	// and no-op if it is set.
	// Checking under the same lock that serialises the writes is what makes
	// this airtight — see PublishOffline's doc comment.
	closed bool

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

// TrackedCount returns how many positions currently have a Home Assistant
// device.
//
// It takes only the fine-grained mu, not publishMu: callers reach this after a
// publish path's PublishSnapshot call has already returned (and so already
// released publishMu), never from inside one, so there is no lock ordering
// against the coarse mutex to reason about.
func (s *Service) TrackedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.discovered)
}

// PublishDiscovery publishes the account device's discovery configs, plus
// placeholder configs for every explicitly whitelisted ticker.
//
// A placeholder names the device after the raw ticker and assumes the account
// currency, because the real instrument metadata only arrives with the first
// poll that sees the position. Publishing it up front means a whitelisted
// holding appears in Home Assistant immediately — greyed out — rather than
// silently missing until it is next held.
func (s *Service) PublishDiscovery(ctx context.Context) error {
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	if s.closed {
		return nil // PublishOffline has run; see its doc comment
	}

	msgs, err := homeassistant.BuildAccountDiscovery(s.cfg)
	if err != nil {
		return err
	}
	if err := s.publishAll(ctx, msgs); err != nil {
		return err
	}

	for _, ticker := range s.wl.Static() {
		if err := s.publishPlaceholder(ctx, ticker); err != nil {
			return err
		}
	}
	return nil
}

// publishPlaceholder emits the startup discovery for a statically whitelisted
// ticker no poll has seen yet: the device named after the raw ticker, the
// account currency standing in for the instrument's, and a retained offline
// because nothing says the position is held (REQ-HA-09).
func (s *Service) publishPlaceholder(ctx context.Context, ticker string) error {
	if err := s.ensurePositionDiscovery(ctx, ticker, "", ""); err != nil {
		return err
	}
	return s.setPositionAvailability(ctx, ticker, false)
}

// Republish restores everything this Service currently knows to the broker. It
// is the MQTT (re)connection handler (REQ-HA-13), and it is deliberately *not*
// PublishDiscovery.
//
// The distinction matters because PublishDiscovery is a startup routine: it
// publishes placeholder discovery for statically whitelisted tickers and forces
// each of them offline, which is correct exactly once, before the first poll.
// Running it on a reconnect would overwrite a position's refined discovery with
// the placeholder — renaming its Home Assistant device and reverting
// avg_price/current_price from the instrument currency to the account currency,
// which HA's recorder treats as a long-term-statistics unit mismatch — and mark
// a currently held position offline. With TICKERS=* it is worse still: the
// static list is empty, so nothing position-related is republished at all, and
// the in-process discovery/availability memos make every later poll
// short-circuit too, so a broker that lost its retained set never gets the
// position devices back.
//
// Republish therefore restores current known state rather than rerunning
// startup:
//
//   - account discovery;
//   - for each tracked position, discovery rebuilt from the stored instrument
//     name and currency, so a refined position republishes as refined;
//   - each tracked position's last-known availability, so a held position comes
//     back online;
//   - placeholder discovery plus offline for statically whitelisted tickers
//     that have never been seen — the startup behaviour that is correct to keep,
//     and which matters when the first connection-up beats the initial
//     PublishDiscovery call.
//
// Every publish here is unconditional: the "unchanged, skip it" memos that
// PublishSnapshot relies on are exactly what would defeat healing a broker
// whose retained set is gone.
//
// Like every other entry point it holds publishMu for its whole body and honours
// the closed latch, because it runs from internal/mqtt's connection callback and
// so races both the scheduler's publish and shutdown — see PublishOffline.
func (s *Service) Republish(ctx context.Context) error {
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	if s.closed {
		return nil // PublishOffline has run; see its doc comment
	}

	msgs, err := homeassistant.BuildAccountDiscovery(s.cfg)
	if err != nil {
		return err
	}
	if err := s.publishAll(ctx, msgs); err != nil {
		return err
	}

	entries, online := s.trackedState()
	for _, e := range entries {
		if err := s.publishPositionDiscovery(ctx, e.ticker, e.name, e.instrumentCurrency); err != nil {
			return err
		}
		if err := s.publishPositionAvailability(ctx, e.ticker, online[trading212.Slug(e.ticker)]); err != nil {
			return err
		}
	}

	// Statically whitelisted tickers with no entry above have never been seen,
	// so they still need their startup placeholder.
	for _, ticker := range s.wl.Static() {
		if _, tracked := online[trading212.Slug(ticker)]; tracked {
			continue
		}
		if err := s.publishPlaceholder(ctx, ticker); err != nil {
			return err
		}
	}
	return nil
}

// PublishAvailability publishes the service-level online/offline state.
func (s *Service) PublishAvailability(ctx context.Context, online bool) error {
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	if s.closed {
		return nil // PublishOffline has run; see its doc comment
	}

	return s.publishServiceAvailability(ctx, online)
}

// publishServiceAvailability is the unlocked implementation shared by
// PublishAvailability and PublishOffline, both of which hold publishMu for
// their whole body — PublishOffline calls this directly rather than the public
// PublishAvailability to avoid re-locking the non-reentrant publishMu.
func (s *Service) publishServiceAvailability(ctx context.Context, online bool) error {
	return s.pub.Publish(ctx, s.cfg.AvailabilityTopic(), []byte(availabilityPayload(online)), true)
}

// PublishOffline marks the service and every tracked position offline. Used on
// graceful shutdown, where the Last Will alone would not fire.
//
// It iterates discovered, not online, so a placeholder that was never held is
// still explicitly marked offline on shutdown.
//
// PublishOffline is terminal: it is, semantically, the last thing this
// Service ever publishes. It latches closed under publishMu before doing its
// work, so PublishDiscovery, PublishSnapshot and PublishAvailability all
// become deliberate no-ops from this point on — including for a reconnect
// callback that was mid-flight when shutdown began and only gets to acquire
// publishMu afterwards. Do not remove this guard: without it, such a callback
// can republish "online" over the "offline" written here, with nothing left
// to ever correct it.
func (s *Service) PublishOffline(ctx context.Context) error {
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	s.closed = true

	// Every position is attempted and the failures aggregated, and the service
	// topic is written unconditionally afterwards. Returning on the first
	// position failure would skip the service topic — the one that greys out
	// *everything* — while closed is already latched so nothing could retry it,
	// and serve.go's clean Disconnect suppresses the Last Will that would
	// otherwise cover for it. Home Assistant would then show a dead service as
	// healthy, indefinitely.
	var errs []error
	for _, t := range s.trackedTickers() {
		if err := s.setPositionAvailability(ctx, t, false); err != nil {
			errs = append(errs, err)
		}
	}
	if err := s.publishServiceAvailability(ctx, false); err != nil {
		errs = append(errs, fmt.Errorf("publish service availability: %w", err))
	}
	return errors.Join(errs...)
}

// PublishSnapshot publishes one poll's worth of state: the account document,
// then each tracked position's document and availability.
//
// Positions are filtered client-side against the whitelist. A tracked ticker
// that is not in the snapshot is marked offline but keeps its discovery, so
// selling out and buying back in needs no discovery churn.
func (s *Service) PublishSnapshot(ctx context.Context, snap trading212.Snapshot) error {
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	if s.closed {
		return nil // PublishOffline has run; see its doc comment
	}

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

	s.mu.Lock()
	entry, seen := s.discovered[slug]
	s.mu.Unlock()
	if seen && entry.matches(name, instrumentCurrency) {
		return nil
	}
	return s.publishPositionDiscovery(ctx, ticker, name, instrumentCurrency)
}

// publishPositionDiscovery publishes a position's discovery configs
// unconditionally and records the metadata they were built from. It is the
// forced counterpart to ensurePositionDiscovery, used by Republish, where the
// "unchanged, skip it" memo is precisely what would stop a broker that lost its
// retained set from ever being healed.
func (s *Service) publishPositionDiscovery(ctx context.Context, ticker, name, instrumentCurrency string) error {
	msgs, err := homeassistant.BuildPositionDiscovery(s.cfg, ticker, name, instrumentCurrency)
	if err != nil {
		return err
	}
	if err := s.publishAll(ctx, msgs); err != nil {
		return err
	}

	s.mu.Lock()
	s.discovered[trading212.Slug(ticker)] = discoveryEntry{
		ticker:             ticker,
		name:               name,
		instrumentCurrency: instrumentCurrency,
	}
	s.mu.Unlock()
	return nil
}

// setPositionAvailability publishes a position's availability, skipping the
// publish when it has not changed.
func (s *Service) setPositionAvailability(ctx context.Context, ticker string, online bool) error {
	s.mu.Lock()
	prev, seen := s.online[trading212.Slug(ticker)]
	unchanged := seen && prev == online
	s.mu.Unlock()
	if unchanged {
		return nil
	}
	return s.publishPositionAvailability(ctx, ticker, online)
}

// publishPositionAvailability publishes a position's availability
// unconditionally and records it. Forced counterpart to
// setPositionAvailability, for the same reason publishPositionDiscovery exists.
func (s *Service) publishPositionAvailability(ctx context.Context, ticker string, online bool) error {
	topic := s.cfg.PositionAvailabilityTopic(ticker)
	if err := s.pub.Publish(ctx, topic, []byte(availabilityPayload(online)), true); err != nil {
		return fmt.Errorf("publish availability %s: %w", ticker, err)
	}

	s.mu.Lock()
	s.online[trading212.Slug(ticker)] = online
	s.mu.Unlock()
	return nil
}

// trackedTickers returns the original ticker string for every position that
// has discovery published, recovered from discovered's slug-keyed entries so
// callers can rebuild topics and device names correctly.
// trackedState returns a consistent snapshot of every tracked position's
// discovery metadata and last-published availability, taken under a single hold
// of mu so Republish rebuilds from one coherent view rather than interleaving
// reads with a concurrent writer.
func (s *Service) trackedState() ([]discoveryEntry, map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries := make([]discoveryEntry, 0, len(s.discovered))
	for _, e := range s.discovered {
		entries = append(entries, e)
	}
	online := make(map[string]bool, len(s.discovered))
	for slug := range s.discovered {
		online[slug] = s.online[slug]
	}
	return entries, online
}

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
