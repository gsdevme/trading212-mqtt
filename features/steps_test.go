package features

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/cucumber/godog"

	"github.com/gsdevme/trading212-mqtt/internal/homeassistant"
	"github.com/gsdevme/trading212-mqtt/internal/mock"
	"github.com/gsdevme/trading212-mqtt/internal/publisher"
	"github.com/gsdevme/trading212-mqtt/internal/scheduler"
	"github.com/gsdevme/trading212-mqtt/internal/server"
	"github.com/gsdevme/trading212-mqtt/internal/trading212"
)

// world holds per-scenario state. It mirrors the wiring in internal/cmd/serve.go
// so the suite exercises the real composition, not a simplified stand-in.
type world struct {
	opts    mock.Options
	tickers string

	mock   *mock.Server
	srv    *httptest.Server
	client *trading212.Client
	rec    *publisher.RecordingPublisher
	pub    *publisher.Service
	status *server.Server
	sched  *scheduler.Scheduler
	haCfg  homeassistant.Config
	ctx    context.Context
}

func (w *world) reset() {
	w.opts = mock.Defaults()
	w.tickers = ""
	w.mock = nil
	w.srv = nil
	w.ctx = context.Background()
}

func (w *world) cleanup() {
	if w.srv != nil {
		w.srv.Close()
	}
}

// --- Given ---

func (w *world) gbpAccountWithThreeHoldings() error {
	w.opts = mock.Defaults()
	return nil
}

func (w *world) trackedTickersAre(tickers string) error {
	w.tickers = tickers
	return nil
}

// --- When ---

func (w *world) serviceStartsUp() error {
	w.mock = mock.New(w.opts)
	w.srv = httptest.NewServer(w.mock.Handler())

	client, err := trading212.New(trading212.Config{
		BaseURL:   w.srv.URL + "/api/v0",
		APIKey:    "test-key",
		APISecret: "test-secret",
	})
	if err != nil {
		return err
	}
	w.client = client

	account, err := client.AccountSummary(w.ctx)
	if err != nil {
		return fmt.Errorf("validate credentials: %w", err)
	}

	w.haCfg = homeassistant.Config{
		DiscoveryPrefix: "homeassistant",
		TopicPrefix:     "trading212",
		AccountID:       account.ID,
		Currency:        account.Currency,
	}
	w.rec = publisher.NewRecordingPublisher()
	w.pub = publisher.New(w.rec, w.haCfg, trading212.ParseWhitelist(w.tickers))
	w.status = server.New(server.Config{ReadyFailureThreshold: 3, Mode: "mock"})
	w.status.SetAccount(account.ID, account.Currency)

	if err := w.pub.PublishDiscovery(w.ctx); err != nil {
		return err
	}
	if err := w.pub.PublishAvailability(w.ctx, true); err != nil {
		return err
	}

	w.sched = scheduler.New(w.client, w.pub, w.status, scheduler.Config{
		AccountCurrency: account.Currency,
		MaxRetries:      0,
	})
	return nil
}

func (w *world) aPollRuns() error {
	w.sched.PollNow(w.ctx)
	return nil
}

func (w *world) nPollsRun(n int) error {
	for i := 0; i < n; i++ {
		w.sched.PollNow(w.ctx)
	}
	return nil
}

// accountTotalValueChangesTo moves the mock's total value so a poll that
// actually publishes can be told apart from one that correctly skipped. Without
// it, "retained state survives a failed poll" passes even if the poll never
// failed, because the unchanged value would be republished identically.
func (w *world) accountTotalValueChangesTo(v float64) error {
	opts := w.opts
	opts.TotalValue = v
	w.opts = opts
	w.mock.SetOptions(opts)
	return nil
}

func (w *world) theAPIStartsFailing() error {
	w.mock.SetFail(true)
	return nil
}

func (w *world) accountNoLongerHolds(ticker string) error {
	opts := w.opts
	kept := opts.Positions[:0:0]
	for _, p := range opts.Positions {
		if p.Ticker != ticker {
			kept = append(kept, p)
		}
	}
	opts.Positions = kept
	w.opts = opts
	w.mock.SetOptions(opts)
	return nil
}

// --- Then ---

func (w *world) accountStateReports(field string, want float64) error {
	m, err := w.decodeTopic(w.haCfg.StateTopic())
	if err != nil {
		return err
	}
	got, ok := m[field].(float64)
	if !ok {
		return fmt.Errorf("account state has no numeric %q (got %v)", field, m[field])
	}
	if got != want {
		return fmt.Errorf("%s = %v, want %v", field, got, want)
	}
	return nil
}

func (w *world) accountDiscoveryPublishedRetained() error {
	topic := fmt.Sprintf("homeassistant/sensor/%s/total_value/config", w.haCfg.DeviceID())
	rec, ok := w.rec.Get(topic)
	if !ok {
		return fmt.Errorf("missing %s; topics: %v", topic, w.rec.Topics())
	}
	if !rec.Retain {
		return fmt.Errorf("%s was not retained", topic)
	}
	return nil
}

func (w *world) accountDeviceHasNoViaDevice() error {
	topic := fmt.Sprintf("homeassistant/sensor/%s/total_value/config", w.haCfg.DeviceID())
	p, err := w.decodeTopic(topic)
	if err != nil {
		return err
	}
	dev, _ := p["device"].(map[string]any)
	if _, has := dev["via_device"]; has {
		return fmt.Errorf("account device must not have a via_device")
	}
	return nil
}

func (w *world) availabilityPublishedRetained(payload string) error {
	rec, ok := w.rec.Get(w.haCfg.AvailabilityTopic())
	if !ok {
		return fmt.Errorf("missing availability topic")
	}
	if string(rec.Payload) != payload {
		return fmt.Errorf("availability = %q, want %q", rec.Payload, payload)
	}
	if !rec.Retain {
		return fmt.Errorf("availability was not retained")
	}
	return nil
}

func (w *world) availabilityPublishedFor(payload, ticker string) error {
	topic := w.haCfg.PositionAvailabilityTopic(ticker)
	rec, ok := w.rec.Get(topic)
	if !ok {
		return fmt.Errorf("missing %s; topics: %v", topic, w.rec.Topics())
	}
	if string(rec.Payload) != payload {
		return fmt.Errorf("%s = %q, want %q", topic, rec.Payload, payload)
	}
	return nil
}

func (w *world) stateDocumentPublishedFor(ticker string) error {
	topic := w.haCfg.PositionStateTopic(ticker)
	if _, ok := w.rec.Get(topic); !ok {
		return fmt.Errorf("missing %s; topics: %v", topic, w.rec.Topics())
	}
	return nil
}

func (w *world) noStateDocumentPublishedFor(ticker string) error {
	topic := w.haCfg.PositionStateTopic(ticker)
	if _, ok := w.rec.Get(topic); ok {
		return fmt.Errorf("unexpected %s for an untracked ticker", topic)
	}
	return nil
}

func (w *world) discoveryConfigExistsFor(ticker string) error {
	topic := fmt.Sprintf("homeassistant/sensor/%s/value/config", w.haCfg.PositionDeviceID(ticker))
	if _, ok := w.rec.Get(topic); !ok {
		return fmt.Errorf("missing %s; topics: %v", topic, w.rec.Topics())
	}
	return nil
}

func (w *world) deviceLinkedToAccountDevice(ticker string) error {
	topic := fmt.Sprintf("homeassistant/sensor/%s/value/config", w.haCfg.PositionDeviceID(ticker))
	p, err := w.decodeTopic(topic)
	if err != nil {
		return err
	}
	dev, _ := p["device"].(map[string]any)
	if dev["via_device"] != w.haCfg.DeviceID() {
		return fmt.Errorf("via_device = %v, want %s", dev["via_device"], w.haCfg.DeviceID())
	}
	return nil
}

func (w *world) entityDenominatedIn(ticker, entity, currency string) error {
	topic := fmt.Sprintf("homeassistant/sensor/%s/%s/config", w.haCfg.PositionDeviceID(ticker), entity)
	p, err := w.decodeTopic(topic)
	if err != nil {
		return err
	}
	if p["unit_of_measurement"] != currency {
		return fmt.Errorf("%s unit = %v, want %s", entity, p["unit_of_measurement"], currency)
	}
	return nil
}

func (w *world) stateReportsNullReturn(ticker string) error {
	m, err := w.decodeTopic(w.haCfg.PositionStateTopic(ticker))
	if err != nil {
		return err
	}
	v, present := m["return_pct"]
	if !present {
		return fmt.Errorf("return_pct is missing entirely; it must be present and null")
	}
	if v != nil {
		return fmt.Errorf("return_pct = %v, want null for a zero cost basis", v)
	}
	return nil
}

func (w *world) readinessReportsReady() error {
	if !w.status.Ready() {
		return fmt.Errorf("expected ready")
	}
	return nil
}

func (w *world) readinessReportsNotReady() error {
	if w.status.Ready() {
		return fmt.Errorf("expected not ready")
	}
	return nil
}

func (w *world) decodeTopic(topic string) (map[string]any, error) {
	rec, ok := w.rec.Get(topic)
	if !ok {
		return nil, fmt.Errorf("missing topic %s; topics: %v", topic, w.rec.Topics())
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Payload, &m); err != nil {
		return nil, fmt.Errorf("decode %s: %w", topic, err)
	}
	return m, nil
}

// --- wiring ---

func InitializeScenario(sc *godog.ScenarioContext) {
	w := &world{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		w.reset()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		w.cleanup()
		return ctx, nil
	})

	sc.Given(`^a GBP account holding AAPL_US_EQ, VUSA_EQ and FREE_EQ$`, w.gbpAccountWithThreeHoldings)
	sc.Given(`^the tracked tickers are "([^"]*)"$`, w.trackedTickersAre)

	sc.When(`^the service starts up$`, w.serviceStartsUp)
	sc.When(`^a poll runs$`, w.aPollRuns)
	sc.When(`^(\d+) polls run$`, w.nPollsRun)
	sc.When(`^the account total value changes to ([0-9.]+)$`, w.accountTotalValueChangesTo)
	sc.When(`^the API starts failing$`, w.theAPIStartsFailing)
	sc.When(`^the account no longer holds "([^"]*)"$`, w.accountNoLongerHolds)

	sc.Then(`^the account total value discovery config is published retained$`, w.accountDiscoveryPublishedRetained)
	sc.Then(`^the account device has no via_device$`, w.accountDeviceHasNoViaDevice)
	sc.Then(`^availability "([^"]*)" is published retained$`, w.availabilityPublishedRetained)
	sc.Then(`^availability "([^"]*)" is published for "([^"]*)"$`, w.availabilityPublishedFor)
	sc.Then(`^the account state reports ([a-z_]+) ([0-9.]+)$`, w.accountStateReports)
	sc.Then(`^a state document is published for "([^"]*)"$`, w.stateDocumentPublishedFor)
	sc.Then(`^no state document is published for "([^"]*)"$`, w.noStateDocumentPublishedFor)
	sc.Then(`^a discovery config exists for "([^"]*)"$`, w.discoveryConfigExistsFor)
	sc.Then(`^the "([^"]*)" device is linked to the account device$`, w.deviceLinkedToAccountDevice)
	sc.Then(`^the "([^"]*)" entity "([^"]*)" is denominated in "([^"]*)"$`, w.entityDenominatedIn)
	sc.Then(`^the "([^"]*)" state reports a null return_pct$`, w.stateReportsNullReturn)
	sc.Then(`^the readiness endpoint reports ready$`, w.readinessReportsReady)
	sc.Then(`^the readiness endpoint reports not ready$`, w.readinessReportsNotReady)
}

func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"."},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("acceptance suite failed")
	}
}
