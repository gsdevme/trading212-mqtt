package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsdevme/trading212-mqtt/internal/mock"
	"github.com/gsdevme/trading212-mqtt/internal/mqtt"
)

// These tests drive the real composition root — runServeWith — in-process,
// against the in-process mock API and a fake broker. They exist because every
// defect found at this seam so far (teardown before PublishOffline, the
// <-schedDone hang, and the reconnect handler rerunning startup) is invisible
// to every package-level suite by construction: each component behaves exactly
// as its own tests specify and the bug lives entirely in how they are wired.

const (
	accountID          = "12345678"
	svcAvailTopic      = "trading212/" + accountID + "/availability"
	aaplAvailTopic     = "trading212/" + accountID + "/positions/aapl_us_eq/availability"
	aaplAvgPriceConfig = "homeassistant/sensor/t212_" + accountID + "_aapl_us_eq/avg_price/config"
	freeAvailTopic     = "trading212/" + accountID + "/positions/free_eq/availability"
)

// --- fake broker -------------------------------------------------------------

// mqttEvent is one observable thing the transport was asked to do. The ordered
// log is the point: the shutdown assertions are about sequence, not just final
// state.
type mqttEvent struct {
	kind    string // "publish" | "rejected" | "disconnect" | "teardown"
	topic   string
	payload string
	retain  bool
}

// fakeMQTT satisfies mqttTransport and records both an ordered event log and
// the retained-message state a real broker would hold.
type fakeMQTT struct {
	mu     sync.Mutex
	events []mqttEvent
	state  map[string]mqttEvent
	reject map[string]bool
	onUp   func(context.Context)

	// teardown closes when the context the transport was constructed with is
	// cancelled — i.e. when runServeWith tears the connection manager down.
	teardown chan struct{}
}

func newFakeMQTT(ctx context.Context) *fakeMQTT {
	f := &fakeMQTT{
		state:    map[string]mqttEvent{},
		reject:   map[string]bool{},
		teardown: make(chan struct{}),
	}
	go func() {
		<-ctx.Done()
		f.mu.Lock()
		f.events = append(f.events, mqttEvent{kind: "teardown"})
		f.mu.Unlock()
		close(f.teardown)
	}()
	return f
}

func (f *fakeMQTT) Publish(_ context.Context, topic string, payload []byte, retain bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reject[topic] {
		f.events = append(f.events, mqttEvent{kind: "rejected", topic: topic})
		return fmt.Errorf("broker rejected %s", topic)
	}
	e := mqttEvent{kind: "publish", topic: topic, payload: string(payload), retain: retain}
	f.events = append(f.events, e)
	f.state[topic] = e
	return nil
}

func (f *fakeMQTT) SetOnConnectionUp(cb func(ctx context.Context)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onUp = cb
}

func (f *fakeMQTT) Disconnect(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, mqttEvent{kind: "disconnect"})
	return nil
}

// fireOnUp invokes the registered reconnect callback synchronously, standing in
// for autopaho's OnConnectionUp after a broker restart. Synchronous so the
// tests are deterministic; the callback itself is identical.
func (f *fakeMQTT) fireOnUp(ctx context.Context) {
	f.mu.Lock()
	cb := f.onUp
	f.mu.Unlock()
	if cb != nil {
		cb(ctx)
	}
}

// rejectTopic makes every subsequent publish to topic fail, standing in for a
// broker ACL denial or an unacked QoS 1 publish.
func (f *fakeMQTT) rejectTopic(topic string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reject[topic] = true
}

// clearRetained drops the retained set while leaving the event log intact —
// exactly what a broker restarted without persistence does.
func (f *fakeMQTT) clearRetained() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = map[string]mqttEvent{}
}

func (f *fakeMQTT) retained(topic string) (mqttEvent, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.state[topic]
	return e, ok
}

func (f *fakeMQTT) eventLog() []mqttEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mqttEvent(nil), f.events...)
}

// waitForPayload blocks until topic holds want, or fails the test.
func (f *fakeMQTT) waitForPayload(t *testing.T, topic, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if e, ok := f.retained(topic); ok && e.payload == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	e, ok := f.retained(topic)
	t.Fatalf("timed out waiting for %s = %q (present=%v, got %q)", topic, want, ok, e.payload)
}

// mustRetained fails unless topic currently holds want, retained.
func (f *fakeMQTT) mustRetained(t *testing.T, topic, want string) {
	t.Helper()
	e, ok := f.retained(topic)
	if !ok {
		t.Fatalf("%s was never published", topic)
	}
	if e.payload != want {
		t.Errorf("%s = %q, want %q", topic, e.payload, want)
	}
	if !e.retain {
		t.Errorf("%s was not published retained", topic)
	}
}

// --- harness -----------------------------------------------------------------

type serveHarness struct {
	broker *fakeMQTT
	cancel context.CancelFunc
	ctx    context.Context

	// returned closes once runServeWith has returned; err is then readable.
	// A channel rather than a result channel so both waitForReturn and the
	// t.Cleanup unwind can observe it.
	returned chan struct{}
	err      error
}

// startServe boots the real composition root against the in-process mock API
// and a fake broker, and blocks until the transport has been constructed.
//
// The API is redirected via MODE=mock + MOCK_URL, which is a supported
// production path (see internal/config), not a test-only hook.
func startServe(t *testing.T, extraEnv map[string]string) *serveHarness {
	t.Helper()

	api := httptest.NewServer(mock.New(mock.Defaults()).Handler())
	t.Cleanup(api.Close)

	env := map[string]string{
		"MODE":                    "mock",
		"MOCK_URL":                api.URL,
		"MQTT_BROKER_URL":         "mqtt://fake-broker:1883",
		"TICKERS":                 "",
		"POLL_INTERVAL":           "1m",
		"POLL_MAX_RETRIES":        "0",
		"READY_FAILURE_THRESHOLD": "3",
		"HTTP_ADDR":               "127.0.0.1:0",
		"LOG_LEVEL":               "error",
		"LOG_FORMAT":              "text",
		"TOPIC_PREFIX":            "trading212",
		"DISCOVERY_PREFIX":        "homeassistant",
		"T212_API_KEY":            "",
		"T212_API_SECRET":         "",
		"MQTT_CLIENT_ID":          "",
		"MQTT_USERNAME":           "",
		"MQTT_PASSWORD":           "",
	}
	for k, v := range extraEnv {
		env[k] = v
	}
	for k, v := range env {
		t.Setenv(k, v)
	}

	h := &serveHarness{returned: make(chan struct{})}
	h.ctx, h.cancel = context.WithCancel(context.Background())

	connected := make(chan *fakeMQTT, 1)
	deps := serveDeps{
		connectMQTT: func(ctx context.Context, _ mqtt.Options) (mqttTransport, error) {
			f := newFakeMQTT(ctx)
			connected <- f
			return f, nil
		},
	}

	go func() {
		h.err = runServeWith(h.ctx, deps)
		close(h.returned)
	}()

	// Cleanup runs before the mock API's, so an aborted test still unwinds the
	// service rather than leaving it polling a closed server.
	t.Cleanup(func() {
		h.cancel()
		select {
		case <-h.returned:
		case <-time.After(15 * time.Second):
			t.Error("runServeWith did not return during cleanup")
		}
	})

	select {
	case h.broker = <-connected:
	case <-h.returned:
		t.Fatalf("runServeWith returned before connecting: %v", h.err)
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for the MQTT transport to be constructed")
	}
	return h
}

// waitForReturn asserts runServeWith returns within a bounded time and yields
// its error. Because runServeWith receives from schedDone before returning, a
// bounded return *is* the assertion that the scheduler goroutine finished and
// schedDone was closed — the regression guard for the <-schedDone hang.
func (h *serveHarness) waitForReturn(t *testing.T) error {
	t.Helper()
	select {
	case <-h.returned:
		return h.err
	case <-time.After(15 * time.Second):
		t.Fatal("runServeWith did not return (schedDone never closed?)")
		return nil
	}
}

// indexOf returns the position of the first event matching pred, or -1.
func indexOf(events []mqttEvent, pred func(mqttEvent) bool) int {
	for i, e := range events {
		if pred(e) {
			return i
		}
	}
	return -1
}

func lastIndexOf(events []mqttEvent, pred func(mqttEvent) bool) int {
	last := -1
	for i, e := range events {
		if pred(e) {
			last = i
		}
	}
	return last
}

func offlineOn(topic string) func(mqttEvent) bool {
	return func(e mqttEvent) bool {
		return e.kind == "publish" && e.topic == topic && e.payload == "offline"
	}
}

func decodeConfig(t *testing.T, e mqttEvent) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(e.payload), &m); err != nil {
		t.Fatalf("decode discovery payload for %s: %v", e.topic, err)
	}
	return m
}

// --- assertions --------------------------------------------------------------

// REQ-LC-05 / REQ-HA-06: every retained offline must land before the clean
// disconnect (which suppresses the Last Will), and the connection manager must
// only be torn down after that. Regression guard for the teardown-before-
// PublishOffline defect, which had no test.
func TestServeShutdownPublishesOfflineBeforeDisconnectAndTeardown(t *testing.T) {
	h := startServe(t, map[string]string{"TICKERS": "AAPL_US_EQ"})
	h.broker.waitForPayload(t, aaplAvailTopic, "online")

	h.cancel()
	if err := h.waitForReturn(t); err != nil {
		t.Fatalf("runServeWith on SIGTERM = %v, want nil (REQ-LC-05 requires exit 0)", err)
	}
	select {
	case <-h.broker.teardown:
	case <-time.After(10 * time.Second):
		t.Fatal("the connection manager context was never cancelled")
	}

	h.broker.mustRetained(t, svcAvailTopic, "offline")
	h.broker.mustRetained(t, aaplAvailTopic, "offline")

	events := h.broker.eventLog()
	svcOffline := lastIndexOf(events, offlineOn(svcAvailTopic))
	posOffline := lastIndexOf(events, offlineOn(aaplAvailTopic))
	disconnect := indexOf(events, func(e mqttEvent) bool { return e.kind == "disconnect" })
	teardown := indexOf(events, func(e mqttEvent) bool { return e.kind == "teardown" })

	for name, idx := range map[string]int{
		"service offline":  svcOffline,
		"position offline": posOffline,
		"disconnect":       disconnect,
		"teardown":         teardown,
	} {
		if idx < 0 {
			t.Fatalf("%s never happened; log = %v", name, events)
		}
	}
	if svcOffline > disconnect || posOffline > disconnect {
		t.Errorf("offline publishes (service=%d position=%d) must precede disconnect (%d); log = %v",
			svcOffline, posOffline, disconnect, events)
	}
	if disconnect > teardown {
		t.Errorf("disconnect (%d) must precede connection-manager teardown (%d); log = %v",
			disconnect, teardown, events)
	}
}

// REQ-HA-13 arm A. A broker reconnect must restore the state the service
// already knows, not rerun startup: a held position stays online, and its
// price entities keep the *instrument* currency and the real device name. On
// HEAD the reconnect callback reran PublishDiscovery, which reverted both —
// and a unit_of_measurement change on an entity carrying state_class breaks
// Home Assistant's long-term statistics.
func TestReconnectKeepsHeldPositionOnlineAndRefined(t *testing.T) {
	h := startServe(t, map[string]string{"TICKERS": "AAPL_US_EQ"})
	h.broker.waitForPayload(t, aaplAvailTopic, "online")
	h.broker.waitForPayload(t, svcAvailTopic, "online")

	h.broker.fireOnUp(h.ctx)

	h.broker.mustRetained(t, svcAvailTopic, "online")
	h.broker.mustRetained(t, aaplAvailTopic, "online")

	cfgMsg, ok := h.broker.retained(aaplAvgPriceConfig)
	if !ok {
		t.Fatalf("%s was never published", aaplAvgPriceConfig)
	}
	p := decodeConfig(t, cfgMsg)
	if p["unit_of_measurement"] != "USD" {
		t.Errorf("avg_price unit = %v after a reconnect, want the instrument currency USD", p["unit_of_measurement"])
	}
	dev, _ := p["device"].(map[string]any)
	if dev["name"] != "Trading 212 – Apple Inc." {
		t.Errorf("device name = %v after a reconnect, want the refined instrument name", dev["name"])
	}
}

// REQ-HA-13 arm B. With TICKERS=* the whitelist has no static entries, so a
// reconnect that reran startup republished nothing position-related at all —
// and the in-process discovery/availability memos made every later poll
// short-circuit too, so a broker that lost its retained set never got the
// position devices back. Simulated here by dropping the retained set and firing
// the reconnect callback.
func TestReconnectRepublishesPositionsWhenTrackingEverything(t *testing.T) {
	h := startServe(t, map[string]string{"TICKERS": "*"})
	h.broker.waitForPayload(t, aaplAvailTopic, "online")
	h.broker.waitForPayload(t, freeAvailTopic, "online")

	h.broker.clearRetained()
	h.broker.fireOnUp(h.ctx)

	for _, topic := range []string{aaplAvgPriceConfig, svcAvailTopic, aaplAvailTopic, freeAvailTopic} {
		if _, ok := h.broker.retained(topic); !ok {
			t.Errorf("%s was not republished after the broker lost its retained set", topic)
		}
	}
	h.broker.mustRetained(t, svcAvailTopic, "online")
	h.broker.mustRetained(t, aaplAvailTopic, "online")
	h.broker.mustRetained(t, freeAvailTopic, "online")

	if cfgMsg, ok := h.broker.retained(aaplAvgPriceConfig); ok {
		p := decodeConfig(t, cfgMsg)
		if p["unit_of_measurement"] != "USD" {
			t.Errorf("republished avg_price unit = %v, want USD", p["unit_of_measurement"])
		}
	}
}

// runServeWith must return — and drain its scheduler goroutine — in both
// branches of the wake-up select, not only on SIGTERM. The HTTP-failure branch
// is the one where the root context is never cancelled at all, which is what
// made an earlier version block forever on <-schedDone.
func TestServeReturnsInBothWakeUpBranches(t *testing.T) {
	t.Run("context cancellation", func(t *testing.T) {
		h := startServe(t, map[string]string{"TICKERS": "AAPL_US_EQ"})
		h.broker.waitForPayload(t, aaplAvailTopic, "online")

		h.cancel()
		if err := h.waitForReturn(t); err != nil {
			t.Fatalf("runServeWith = %v, want nil", err)
		}
	})

	t.Run("http listener failure", func(t *testing.T) {
		// Occupy the address so ListenAndServe fails immediately, waking the
		// select on httpErr with the root context still live.
		occupied, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		t.Cleanup(func() { _ = occupied.Close() })

		h := startServe(t, map[string]string{
			"TICKERS":   "AAPL_US_EQ",
			"HTTP_ADDR": occupied.Addr().String(),
		})

		serveErr := h.waitForReturn(t)
		if serveErr == nil {
			t.Fatal("runServeWith = nil, want the listener failure to set a non-zero exit")
		}
		if !strings.Contains(serveErr.Error(), "http server") {
			t.Errorf("error = %v, want it to report the http server failure", serveErr)
		}
		if h.ctx.Err() != nil {
			t.Error("the root context must not have been cancelled in this branch")
		}
		// The shutdown sequence must still have run.
		select {
		case <-h.broker.teardown:
		case <-time.After(10 * time.Second):
			t.Fatal("the connection manager was never torn down")
		}
		h.broker.mustRetained(t, svcAvailTopic, "offline")
	})
}

// REQ-HA-06 / REQ-LC-05. A single position availability publish failing during
// shutdown must not strand the service topic retained-online: the clean
// Disconnect that follows suppresses the Last Will, and PublishOffline has
// already latched the service closed, so nothing could ever correct it. Every
// position is attempted and the service topic is written unconditionally.
func TestPublishOfflineFailureStillMarksServiceOffline(t *testing.T) {
	h := startServe(t, map[string]string{"TICKERS": "AAPL_US_EQ"})
	h.broker.waitForPayload(t, aaplAvailTopic, "online")
	h.broker.mustRetained(t, svcAvailTopic, "online")

	h.broker.rejectTopic(aaplAvailTopic)

	h.cancel()
	if err := h.waitForReturn(t); err != nil {
		t.Fatalf("runServeWith = %v, want nil — a rejected publish is logged, not fatal", err)
	}

	h.broker.mustRetained(t, svcAvailTopic, "offline")

	events := h.broker.eventLog()
	if indexOf(events, func(e mqttEvent) bool { return e.kind == "rejected" && e.topic == aaplAvailTopic }) < 0 {
		t.Fatalf("the position availability publish was never attempted during shutdown; log = %v", events)
	}
	if lastIndexOf(events, offlineOn(svcAvailTopic)) < 0 {
		t.Error("the service availability topic was never written offline")
	}
}
