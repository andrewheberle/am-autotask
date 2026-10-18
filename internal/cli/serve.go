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
	"go.opentelemetry.io/otel"

	"github.com/andrewheberle/am-autotask/internal/autotask"
	"github.com/andrewheberle/am-autotask/internal/config"
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

	g := &gate{}

	// read by metrics scrapes, which may happen before the cache exists
	var cachePtr atomic.Pointer[selector.Cache]
	m := metrics.New(metrics.Options{
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
		Ready: g.ready,
	})

	srv := &http.Server{
		Addr: cfg.Listen,
		Handler: server.New(g, server.Options{
			Token:   cfg.WebhookToken,
			Ready:   g.ready,
			Metrics: m.Handler(),
			Observe: m.ObserveNotification,
			Logger:  logger,

			TracerProvider: tp.TracerProvider,
			Propagators:    tp.Propagators,
		}),
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
		HTTPClient: &http.Client{
			Timeout:   60 * time.Second,
			Transport: tracing.Transport(http.DefaultTransport, tp.TracerProvider),
		},
	})
	if err != nil {
		return err
	}

	if err := client.Validate(ctx, cfg.PicklistChecks(), cfg.Receiver.CatchAllCompanyID); err != nil {
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

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err = srv.Shutdown(shutdownCtx)
	flushed = true
	return errors.Join(err, tp.Shutdown(shutdownCtx))
}
