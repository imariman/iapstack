// Package app composes and runs IAPStack process modes.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/imariman/iapstack/dashboard"
	"github.com/imariman/iapstack/internal/auth"
	"github.com/imariman/iapstack/internal/credentials"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/persistence/postgres"
	"github.com/imariman/iapstack/internal/platform/config"
	"github.com/imariman/iapstack/internal/platform/logging"
	"github.com/imariman/iapstack/internal/platform/metrics"
	platformprotection "github.com/imariman/iapstack/internal/platform/protection"
	"github.com/imariman/iapstack/internal/processing"
	"github.com/imariman/iapstack/internal/stores"
	"github.com/imariman/iapstack/internal/stores/apple"
	"github.com/imariman/iapstack/internal/stores/googleplay"
	"github.com/imariman/iapstack/internal/stores/huawei"
	"github.com/imariman/iapstack/internal/transport/httpapi"
	"github.com/imariman/iapstack/internal/transport/httpserver"
	"github.com/imariman/iapstack/internal/verification"
	"github.com/imariman/iapstack/internal/webhooks"
	"github.com/imariman/iapstack/internal/worker"
)

// systemClock supplies trusted UTC process time to application services.
type systemClock struct{}

var (
	// ErrUsage indicates that the process mode arguments are missing or invalid.
	ErrUsage = errors.New("usage: iapstack <api|worker|migrate>")
)

// Run starts the selected IAPStack process and blocks until it stops.
func Run(
	ctx context.Context,
	args []string,
	getenv func(string) string,
	logOutput io.Writer,
) error {
	if len(args) != 1 {
		return ErrUsage
	}
	mode := args[0]
	if mode != "api" && mode != "worker" && mode != "migrate" {
		return fmt.Errorf("%w: unknown mode %q", ErrUsage, mode)
	}
	cfg, err := config.Load(getenv)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	logger := logging.New(logOutput, cfg.LogLevel)
	logger.Info("starting iapstack", "mode", mode)
	if mode == "migrate" {
		return postgres.Migrate(ctx, cfg.DatabaseURL)
	}

	store, err := postgres.OpenStore(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open runtime persistence: %w", err)
	}
	defer store.Close()
	keyring, err := platformprotection.OpenFromEnvironment(getenv)
	if err != nil {
		return fmt.Errorf("open runtime protection: %w", err)
	}
	credentialService, err := credentials.NewService(store, keyring)
	if err != nil {
		return err
	}
	huaweiAdapter, err := huawei.New(credentialService, cfg.ProviderTimeout)
	if err != nil {
		return err
	}
	appleAdapter, err := apple.New(credentialService, cfg.ProviderTimeout)
	if err != nil {
		return err
	}
	googlePlayAdapter, err := googleplay.New(credentialService, cfg.ProviderTimeout)
	if err != nil {
		return err
	}
	metricRegistry := metrics.New()
	observedHuawei, err := stores.NewObservedAdapter(huaweiAdapter, metricRegistry)
	if err != nil {
		return err
	}
	observedApple, err := stores.NewObservedAdapter(appleAdapter, metricRegistry)
	if err != nil {
		return err
	}
	observedGooglePlay, err := stores.NewObservedAdapter(googlePlayAdapter, metricRegistry)
	if err != nil {
		return err
	}
	registry, err := stores.NewRegistry(observedHuawei, observedApple, observedGooglePlay)
	if err != nil {
		return err
	}
	verificationService, err := verification.NewService(
		store, registry, keyring, verification.NewDefaultProjector(), systemClock{},
	)
	if err != nil {
		return err
	}
	webhookService, err := webhooks.NewService(
		store,
		keyring,
		cfg.WebhookTimeout,
		cfg.WebhookAllowPrivateNetworks,
	)
	if err != nil {
		return err
	}

	switch mode {
	case "api":
		return runAPI(ctx, cfg, store, keyring, credentialService, webhookService,
			verificationService, huaweiAdapter, metricRegistry, logger)
	case "worker":
		return runWorker(ctx, cfg, store, keyring, webhookService, verificationService, metricRegistry, logger)
	default:
		panic("validated mode was not handled")
	}
}

// Now returns current UTC time for provider-neutral verification receipt timestamps.
func (systemClock) Now() time.Time {
	return time.Now().UTC()
}

// runAPI composes versioned handlers with probes, metrics, and graceful HTTP lifecycle.
func runAPI(
	ctx context.Context,
	cfg config.Config,
	store *postgres.Store,
	keyring *platformprotection.Keyring,
	credentialService *credentials.Service,
	webhookService *webhooks.Service,
	verificationService *verification.Service,
	huaweiAdapter *huawei.Adapter,
	metricRegistry *metrics.Registry,
	logger *slog.Logger,
) error {
	authentication, err := auth.NewService(store, cfg.BootstrapAdminKey)
	if err != nil {
		return err
	}
	api, err := httpapi.New(httpapi.Dependencies{
		Store: store, Operations: store, Admin: store, Authentication: authentication,
		Credentials: credentialService, Webhooks: webhookService,
		Verification: verificationService, Huawei: huaweiAdapter,
		Protection: keyring, BodyLimit: cfg.HTTPBodyLimit,
	})
	if err != nil {
		return err
	}
	dashboardHandler, err := dashboard.New(api)
	if err != nil {
		return err
	}
	server := httpserver.NewWithOptions(httpserver.Options{
		Address: cfg.HTTPAddress, ShutdownTimeout: cfg.ShutdownTimeout,
		ReadinessTimeout: cfg.ReadinessTimeout, Logger: logger,
		ReadyChecker: store, Metrics: metricRegistry, Handler: dashboardHandler,
	})
	return server.Run(ctx)
}

// runWorker composes durable provider and webhook handlers under the worker lifecycle.
func runWorker(
	ctx context.Context,
	cfg config.Config,
	store *postgres.Store,
	keyring *platformprotection.Keyring,
	webhookService *webhooks.Service,
	verificationService *verification.Service,
	metricRegistry *metrics.Registry,
	logger *slog.Logger,
) error {
	providerProcessing, err := processing.New(store, keyring, verificationService)
	if err != nil {
		return err
	}
	runner, err := worker.New(store.Pool(), store, worker.Config{
		WorkerID: cfg.WorkerID, PollInterval: cfg.WorkerPollInterval,
		JobTimeout: cfg.WorkerJobTimeout, Concurrency: cfg.WorkerConcurrency,
		ShutdownTimeout: cfg.ShutdownTimeout, MaxAttempts: cfg.WorkerMaxAttempts,
		QueueRetention: cfg.QueueRetention,
	}, map[persistence.QueueName]worker.Handler{
		persistence.QueueInbox:          worker.HandlerFunc(providerProcessing.HandleInbox),
		persistence.QueueReconciliation: worker.HandlerFunc(providerProcessing.HandleReconciliation),
		persistence.QueueOutbox: worker.HandlerFunc(func(ctx context.Context, message persistence.QueueMessage) error {
			err := webhookService.Deliver(ctx, message)
			if err != nil {
				metricRegistry.ObserveWebhook("failed")
				return err
			}
			metricRegistry.ObserveWebhook("delivered")
			return nil
		}),
	}, metricRegistry, logger)
	if err != nil {
		return err
	}
	logger.Info("worker is ready")
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	server := httpserver.NewWithOptions(httpserver.Options{
		Address: cfg.WorkerHTTPAddress, ShutdownTimeout: cfg.ShutdownTimeout,
		ReadinessTimeout: cfg.ReadinessTimeout, Logger: logger,
		ReadyChecker: store, Metrics: metricRegistry,
	})
	results := make(chan error, 2)
	go func() {
		results <- runner.Run(runContext)
	}()
	go func() {
		results <- server.Run(runContext)
	}()
	first := <-results
	cancel()
	second := <-results
	return errors.Join(first, second)
}
