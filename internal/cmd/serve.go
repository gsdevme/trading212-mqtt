package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/gsdevme/trading212-mqtt/internal/config"
	"github.com/gsdevme/trading212-mqtt/internal/homeassistant"
	"github.com/gsdevme/trading212-mqtt/internal/mqtt"
	"github.com/gsdevme/trading212-mqtt/internal/publisher"
	"github.com/gsdevme/trading212-mqtt/internal/scheduler"
	"github.com/gsdevme/trading212-mqtt/internal/server"
	"github.com/gsdevme/trading212-mqtt/internal/trading212"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the poll -> MQTT service",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runServe(cmd.Context())
	},
}

// mqttTransport is the slice of the MQTT client the composition root actually
// uses: publishing, the (re)connection callback, and the clean disconnect.
// Declaring it as an interface is the one seam runServe needs to be driveable
// in-process against a fake broker — see serve_test.go. *mqtt.Client satisfies
// it; nothing else does in production.
type mqttTransport interface {
	publisher.Publisher
	SetOnConnectionUp(f func(ctx context.Context))
	Disconnect(ctx context.Context) error
}

// serveDeps injects the external constructor runServe cannot otherwise be
// tested through. Its zero value is the production wiring, so runServe's
// behaviour is identical whether or not a test supplied anything.
//
// The Trading 212 API needs no entry here: MODE=mock + MOCK_URL already point
// the client at an arbitrary base URL (see internal/config), and that is a
// supported production path rather than a test-only back door.
type serveDeps struct {
	// connectMQTT builds the MQTT transport. Defaults to mqtt.Connect.
	connectMQTT func(ctx context.Context, opts mqtt.Options) (mqttTransport, error)
}

// withDefaults fills in the production constructors for anything unset.
func (d serveDeps) withDefaults() serveDeps {
	if d.connectMQTT == nil {
		d.connectMQTT = func(ctx context.Context, opts mqtt.Options) (mqttTransport, error) {
			return mqtt.Connect(ctx, opts)
		}
	}
	return d
}

func runServe(ctx context.Context) error {
	return runServeWith(ctx, serveDeps{})
}

// runServeWith is the composition root. Everything it wires — startup
// validation, the reconnect callback, the poll loop, the shutdown sequence —
// lives here, which is why it takes its one external constructor as an
// argument rather than calling it directly.
func runServeWith(ctx context.Context, deps serveDeps) error {
	deps = deps.withDefaults()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	logger := newLogger(cfg.LogLevel, cfg.LogFormat)
	logger.Info("starting", "config", cfg.String())
	if cfg.Mode == "mock" {
		logger.Warn("running in MOCK mode; not using the real Trading 212 API")
	}

	// The status server listens immediately so probes answer during init.
	status := server.New(server.Config{
		ReadyFailureThreshold: cfg.ReadyFailureThreshold,
		PollInterval:          cfg.PollInterval,
		Mode:                  cfg.Mode,
	})
	httpSrv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           status.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	httpErr := make(chan error, 1)
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			httpErr <- err
			return
		}
		httpErr <- nil
	}()

	client, err := trading212.New(trading212.Config{
		BaseURL:   cfg.APIBaseURL,
		APIKey:    cfg.APIKey,
		APISecret: cfg.APISecret,
		Logger:    logger,
	})
	if err != nil {
		return err
	}

	// One summary call validates the credentials and learns the account id and
	// primary currency — both prerequisites for building discovery. Bad
	// credentials are fatal here rather than a silent, permanently-unready pod.
	account, err := client.AccountSummary(ctx)
	if err != nil {
		return startupFailure(ctx, httpSrv, logger, fmt.Errorf("validate credentials: %w", err))
	}
	logger.Info("account resolved", "currency", account.Currency)
	status.SetAccount(account.ID, account.Currency)

	haCfg := homeassistant.Config{
		DiscoveryPrefix: cfg.DiscoveryPrefix,
		TopicPrefix:     cfg.TopicPrefix,
		AccountID:       account.ID,
		Currency:        account.Currency,
	}

	// The MQTT connection must outlive the signal context: autopaho ties its
	// teardown to the context it is created with, and cancelling that on SIGTERM
	// destroys the transport before the graceful-shutdown publishes can land.
	// mqttCtx is only cancelled explicitly, after PublishOffline and Disconnect
	// have both completed below.
	mqttCtx, closeMQTT := context.WithCancel(context.Background())
	defer closeMQTT()

	// Because mqttCtx ignores signals, connecting on this goroutine would make
	// startup deaf to SIGTERM: connectMQTT blocks until the broker answers, so an
	// unreachable one hung the process until SIGKILL. Connect off to the side and
	// select on the signal context instead. The channel is buffered so the
	// goroutine can always finish its send.
	type connectOutcome struct {
		client mqttTransport
		err    error
	}
	connectResult := make(chan connectOutcome, 1)
	go func() {
		client, err := deps.connectMQTT(mqttCtx, mqtt.Options{
			BrokerURL:         cfg.MQTTBrokerURL,
			Username:          cfg.MQTTUsername,
			Password:          cfg.MQTTPassword,
			ClientID:          cfg.MQTTClientID,
			AvailabilityTopic: haCfg.AvailabilityTopic(),
			Logger:            logger,
		})
		connectResult <- connectOutcome{client: client, err: err}
	}()

	var mc mqttTransport
	select {
	case result := <-connectResult:
		mc, err = result.client, result.err
	case <-ctx.Done():
		// closeMQTT unblocks a connect that honours cancellation; the timeout
		// bounds this against one that does not. Nothing is published here — the
		// service never came up — so waiting longer buys nothing, and a goroutine
		// still parked in connectMQTT at process exit is harmless where a
		// shutdown that never returns is not.
		closeMQTT()
		select {
		case result := <-connectResult:
			if result.client != nil {
				shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 3*time.Second)
				_ = result.client.Disconnect(shutdownCtx)
				cancelShutdown()
			}
		case <-time.After(3 * time.Second):
		}
		return startupFailure(ctx, httpSrv, logger, fmt.Errorf("mqtt: %w", ctx.Err()))
	}
	if err != nil {
		return startupFailure(ctx, httpSrv, logger, fmt.Errorf("mqtt: %w", err))
	}

	pub := publisher.New(mc, haCfg, cfg.Whitelist)

	// Restore everything the publisher knows on every (re)connection, healing a
	// broker that lost its retained set (REQ-HA-13).
	//
	// This is pub.Republish, deliberately not pub.PublishDiscovery: the latter is
	// a *startup* routine that publishes placeholder discovery and forces every
	// statically whitelisted ticker offline, which on a reconnect would rename a
	// refined position's device, revert its price entities from the instrument
	// currency to the account currency, and grey out a position that is currently
	// held — and which, under TICKERS=*, would republish nothing about positions
	// at all. See Republish's doc comment.
	//
	// cbCtx is mqttCtx (or a context derived from it), which is no longer
	// cancelled by SIGTERM, so this callback could otherwise fire mid-shutdown;
	// the ctx.Err() check below is a cheap early-out to skip pointless work in
	// that case, not what makes this safe. What makes it safe is that
	// publisher.Service.PublishOffline is terminal: it latches closed under the
	// same lock that serialises every publish, so a reconnect callback that loses
	// the race to PublishOffline (however long it stalls) finds
	// PublishAvailability/Republish are no-ops once it does get in — see
	// PublishOffline's doc comment.
	mc.SetOnConnectionUp(func(cbCtx context.Context) {
		if ctx.Err() != nil {
			return // shutting down; skip the pointless work
		}
		if err := pub.PublishAvailability(cbCtx, true); err != nil {
			logger.Warn("republish availability failed", "err", err)
		}
		if err := pub.Republish(cbCtx); err != nil {
			logger.Warn("republish discovery failed", "err", err)
		}
	})
	// Initial publish, in case the first connection-up fired before the callback
	// was registered.
	if err := pub.PublishDiscovery(ctx); err != nil {
		return startupFailure(ctx, httpSrv, logger, fmt.Errorf("publish discovery: %w", err))
	}
	if err := pub.PublishAvailability(ctx, true); err != nil {
		return startupFailure(ctx, httpSrv, logger, fmt.Errorf("publish availability: %w", err))
	}

	sched := scheduler.New(client, statusRecordingPublisher{pub: pub, status: status}, status, scheduler.Config{
		AccountCurrency: account.Currency,
		PollInterval:    cfg.PollInterval,
		MaxRetries:      cfg.PollMaxRetries,
		Logger:          logger,
	})

	// schedCtx is a child of ctx so the scheduler still stops on SIGTERM as
	// before, but cancelSched lets us stop it explicitly too: in the httpErr
	// branch below, ctx (the SIGTERM context) may never be cancelled at all, and
	// without an independent way to unblock scheduler.Run, waiting on schedDone
	// after that branch would hang forever.
	schedCtx, cancelSched := context.WithCancel(ctx)
	defer cancelSched()

	schedDone := make(chan struct{})
	go func() { sched.Run(schedCtx); close(schedDone) }()

	logger.Info("service running", "addr", cfg.HTTPAddr)
	var httpServeErr error
	select {
	case httpServeErr = <-httpErr:
		logger.Warn("http server failed; shutting down", "err", httpServeErr)
	case <-ctx.Done():
		logger.Info("shutting down")
	}

	// Graceful shutdown uses a fresh context: the root one is already cancelled
	// (or, in the httpServeErr branch, was never going to be). mqttCtx (the
	// transport's own lifetime context) is still live at this point — see the
	// comment where it's created — so these publishes and the clean disconnect
	// actually reach the broker instead of racing a signal-triggered teardown.
	//
	// This same sequence now runs whichever branch above woke the select: an
	// HTTP server failure must not skip retained-offline cleanup and leave every
	// per-position availability topic (which has no Last Will of its own)
	// stranded online.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := pub.PublishOffline(shutdownCtx); err != nil {
		logger.Warn("publish offline failed", "err", err)
	}
	if err := mc.Disconnect(shutdownCtx); err != nil {
		logger.Warn("mqtt disconnect failed", "err", err)
	}
	// Only now tear the connection manager itself down.
	closeMQTT()
	cancelSched()
	<-schedDone

	if httpServeErr != nil {
		// The listener already failed and exited on its own; still attempt a
		// clean Shutdown (harmless/idempotent on an already-stopped server) but
		// the original failure is what must set the exit code — do not swallow
		// it.
		if err := shutdownHTTP(httpSrv); err != nil {
			logger.Warn("http shutdown failed", "err", err)
		}
		return fmt.Errorf("http server: %w", httpServeErr)
	}
	return shutdownHTTP(httpSrv)
}

// statusRecordingPublisher publishes a snapshot, then records a display snapshot
// for the status page — but only on success, so a failed publish leaves the last
// good figures on the page.
type statusRecordingPublisher struct {
	pub    *publisher.Service
	status *server.Server
}

func (r statusRecordingPublisher) PublishSnapshot(ctx context.Context, snap trading212.Snapshot) error {
	if err := r.pub.PublishSnapshot(ctx, snap); err != nil {
		return err
	}
	m := metricsFromSnapshot(snap)
	m.TrackedCount = r.pub.TrackedCount()
	r.status.SetMetrics(m)
	return nil
}

// metricsFromSnapshot maps a snapshot into the status page's view model.
// TrackedCount is filled in separately by the caller, from the publisher, since
// the snapshot alone (unfiltered by the whitelist) cannot tell which positions
// actually have a Home Assistant device.
func metricsFromSnapshot(snap trading212.Snapshot) server.Metrics {
	return server.Metrics{
		Currency:      snap.Account.Currency,
		TotalValue:    snap.Account.TotalValue,
		FreeCash:      snap.Account.FreeCash,
		Invested:      snap.Account.Invested,
		UnrealizedPL:  snap.Account.UnrealizedPL,
		ReturnPct:     snap.Account.ReturnPct,
		PositionCount: len(snap.Positions),
		LastUpdated:   snap.Account.LastUpdated,
	}
}

// startupFailure decides what a failure during startup means, for the sites
// that run after the status listener is up but before the shutdown sequence
// exists.
//
// A failure that is only the root context being cancelled is a SIGTERM landing
// mid-startup: a normal termination, not a fault. Reporting it as an error
// would exit 1 on an ordinary pod rescheduling, which REQ-LC-05 forbids.
// Nothing beyond discovery has been published at these sites, and the MQTT
// connection (if any) is torn down without a clean disconnect, so the Last Will
// still marks the service offline.
//
// The listener is shut down either way: it was started before these sites and
// nothing else will stop it.
func startupFailure(ctx context.Context, httpSrv *http.Server, logger *slog.Logger, err error) error {
	if shutErr := shutdownHTTP(httpSrv); shutErr != nil {
		logger.Warn("http shutdown failed", "err", shutErr)
	}
	if ctx.Err() != nil {
		logger.Info("shutting down during startup")
		return nil
	}
	return err
}

func shutdownHTTP(srv *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}
