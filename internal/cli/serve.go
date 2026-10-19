package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/andrewheberle/simplecommand"
	"github.com/bep/simplecobra"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"

	"github.com/andrewheberle/am-autotask/internal/autotask"
	"github.com/andrewheberle/am-autotask/internal/config"
	"github.com/andrewheberle/am-autotask/internal/leader"
	"github.com/andrewheberle/am-autotask/internal/metrics"
	"github.com/andrewheberle/am-autotask/internal/receiver"
	"github.com/andrewheberle/am-autotask/internal/selector"
	"github.com/andrewheberle/am-autotask/internal/server"
	"github.com/andrewheberle/am-autotask/internal/tracing"
)

// serveCommand runs the webhook receiver.
type serveCommand struct {
	version string

	*simplecommand.Command
}

func newServeCommand(version string) *serveCommand {
	return &serveCommand{
		version: version,
		Command: simplecommand.New("serve", "Run the webhook receiver",
			simplecommand.WithArgs(cobra.NoArgs),
		),
	}
}

func (c *serveCommand) Init(cd *simplecobra.Commandeer) error {
	if err := c.Command.Init(cd); err != nil {
		return err
	}
	config.AddFlags(cd.CobraCommand.Flags())
	return nil
}

// gate holds notifications until startup completes; Alertmanager retries on
// the resulting error.
type gate struct {
	receiver atomic.Pointer[receiver.Receiver]
}

var errNotReady = errors.New("receiver is still starting")

func (g *gate) Handle(ctx context.Context, n receiver.Notification) error {
	r := g.receiver.Load()
	if r == nil {
		return errNotReady
	}
	return r.Handle(ctx, n)
}

func (g *gate) ready() bool {
	return g.receiver.Load() != nil
}

func (c *serveCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	cfg, err := config.FromFlags(cd.CobraCommand.Flags())
	if err != nil {
		return err
	}

	logger := slog.New(tracing.LogHandler(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel})))
	slog.SetDefault(logger)

	// from here errors go to the JSON log, so log collectors see them
	if err := c.serve(ctx, cfg, logger); err != nil {
		logger.Error("exiting", "error", err)
		return &reportedError{err}
	}
	return nil
}

func (c *serveCommand) serve(ctx context.Context, cfg config.Config, logger *slog.Logger) error {

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	tp, err := tracing.Setup(ctx, tracing.Options{Exporter: cfg.TracingExporter, ServiceVersion: c.version})
	if err != nil {
		return fmt.Errorf("setting up tracing: %w", err)
	}
	otel.SetTracerProvider(tp.TracerProvider)
	otel.SetTextMapPropagator(tp.Propagators)
	otel.SetErrorHandler(tracing.NewErrorHandler(logger, time.Minute, nil))
	flushed := false
	defer func() {
		// flush spans from a failed startup; a normal shutdown flushes below
		if !flushed {
			flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = tp.Shutdown(flushCtx)
		}
	}()

	zone, _ := time.Now().Zone()
	logger.Info("ticket templates format times in the local time zone", "zone", zone, "TZ", os.Getenv("TZ"))

	g := &gate{}

	// m is set before the elector runs, which is when OnChange is called
	var m *metrics.Metrics
	el, err := newElector(cfg.LeaderElection, logger, func(leader bool) { m.LeaderChanged(leader) })
	if err != nil {
		return err
	}
	ready, leading := g.ready, g.ready
	if el != nil {
		ready = func() bool { return g.ready() && el.Healthy() }
		leading = el.Leading
	}

	// read by metrics scrapes, which may happen before the cache exists
	var cachePtr atomic.Pointer[selector.Cache]
	m = metrics.New(metrics.Options{
		SelectorsAge: func() time.Duration {
			if c := cachePtr.Load(); c != nil {
				return c.Age()
			}
			return 0
		},
		SelectorCounts: func() (int, int) {
			if c := cachePtr.Load(); c != nil {
				return c.Counts()
			}
			return 0, 0
		},
		Ready:  ready,
		Leader: leading,
	})

	serverOpts := server.Options{
		Token:   cfg.WebhookToken,
		Ready:   ready,
		Metrics: m.Handler(),
		Observe: m.ObserveNotification,
		Logger:  logger,

		TracerProvider: tp.TracerProvider,
		Propagators:    tp.Propagators,
	}
	if el != nil {
		serverOpts.Leadership = el
		serverOpts.ObserveForward = m.ObserveForward
		serverOpts.ForwardClient = &http.Client{
			Timeout: 60 * time.Second,
			Transport: otelhttp.NewTransport(http.DefaultTransport,
				otelhttp.WithTracerProvider(tp.TracerProvider),
				otelhttp.WithPropagators(tp.Propagators),
			),
		}
	}
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           server.New(g, serverOpts),
		ReadHeaderTimeout: 10 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() {
		logger.Info("listening", "address", cfg.Listen, "version", c.version)
		serveErr <- srv.ListenAndServe()
	}()

	client, err := autotask.New(ctx, autotask.Options{
		Username:            cfg.Autotask.Username,
		Secret:              cfg.Autotask.Secret,
		IntegrationCode:     cfg.Autotask.IntegrationCode,
		ZoneInformationURL:  cfg.Autotask.ZoneURL,
		CompanySelectorsUDF: cfg.CompanySelectorsUDF,
		RecordedAlertsUDF:   cfg.RecordedAlertsUDF,
		AppliedPriorityUDF:  cfg.AppliedPriorityUDF,
		HTTPClient: &http.Client{
			Timeout:   60 * time.Second,
			Transport: tracing.Transport(http.DefaultTransport, tp.TracerProvider),
		},
	})
	if err != nil {
		return err
	}

	if err := client.Validate(ctx, cfg.PicklistChecks(), cfg.Receiver.CatchAllCompanyID, cfg.Receiver.ProblemCompanyID, cfg.Receiver.WorkType); err != nil {
		return fmt.Errorf("configuration does not match Autotask:\n%w", err)
	}

	cache := selector.NewCache(tracing.Source(client.Companies, tp.TracerProvider), selector.CacheOptions{
		MissRefreshInterval: cfg.MissRefresh,
		Logger:              logger,
	})
	if err := cache.Load(ctx); err != nil {
		return err
	}
	cachePtr.Store(cache)
	go cache.Run(ctx, cfg.Refresh)

	opts := cfg.Receiver
	if opts.FollowSeverity {
		if opts.PriorityLabels, err = priorityLabels(ctx, client); err != nil {
			return err
		}
	}
	opts.Logger = logger
	opts.Observer = m
	opts.TracerProvider = tp.TracerProvider
	at := tracing.Autotask(m.Instrument(client), tp.TracerProvider)
	r, err := receiver.New(at, cache, receiver.NewMemoryLocker(), opts)
	if err != nil {
		return err
	}
	g.receiver.Store(r)
	logger.Info("ready")

	// the elector outlives ctx, so the lease is held until in-flight
	// notifications have drained and only then released
	stopElector := func() {}
	if el != nil {
		elCtx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			el.Run(elCtx)
			close(done)
		}()
		stopElector = func() {
			cancel()
			<-done
		}
	}

	select {
	case err := <-serveErr:
		stopElector()
		return err
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err = srv.Shutdown(shutdownCtx)
	stopElector()
	flushed = true
	return errors.Join(err, tp.Shutdown(shutdownCtx))
}

// newElector returns the leader elector, or nil without leader election. It
// fails unless running in a Kubernetes pod.
func newElector(cfg config.LeaderElection, logger *slog.Logger, onChange func(bool)) (*leader.Elector, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	in := leader.InCluster{}
	namespace := cfg.Namespace
	if namespace == "" {
		var err error
		if namespace, err = in.Namespace(); err != nil {
			return nil, fmt.Errorf("leader election: %w", err)
		}
	}
	client, err := in.Client(namespace, cfg.Lease)
	if err != nil {
		return nil, fmt.Errorf("leader election: %w", err)
	}

	logger.Info("leader election enabled", "namespace", namespace, "lease", cfg.Lease, "identity", cfg.Identity, "address", cfg.Address)
	return leader.New(client, leader.Options{
		Identity: cfg.Identity,
		Address:  cfg.Address,
		OnChange: onChange,
		Logger:   logger,
	})
}

// priorityLabels names each ticket priority ID, for notes about priority
// changes.
func priorityLabels(ctx context.Context, client *autotask.Client) (map[int]string, error) {
	picklists, err := client.Picklists(ctx, "Tickets")
	if err != nil {
		return nil, fmt.Errorf("reading ticket priorities: %w", err)
	}
	labels := map[int]string{}
	for _, v := range picklists["priority"] {
		labels[v.ID] = v.Label
	}
	return labels, nil
}
