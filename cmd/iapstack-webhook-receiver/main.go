package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/imariman/iapstack/internal/webhookreceiver"
)

const (
	// defaultPort is Render's documented default private service port.
	defaultPort = "10000"
	// defaultBodyLimit bounds one webhook payload to one mebibyte.
	defaultBodyLimit int64 = 1 << 20
	// defaultTimestampTolerance rejects deliveries outside a five-minute replay window.
	defaultTimestampTolerance = 5 * time.Minute
	// defaultShutdownTimeout bounds graceful receiver shutdown.
	defaultShutdownTimeout = 10 * time.Second
	// defaultReadHeaderTimeout bounds request header delivery.
	defaultReadHeaderTimeout = 5 * time.Second
	// defaultReadTimeout bounds complete inbound requests.
	defaultReadTimeout = 10 * time.Second
	// defaultWriteTimeout bounds receiver responses.
	defaultWriteTimeout = 10 * time.Second
	// defaultIdleTimeout bounds keep-alive connections.
	defaultIdleTimeout = 60 * time.Second
	// defaultMaxHeaderBytes bounds attacker-controlled request headers.
	defaultMaxHeaderBytes = 32 << 10
)

// main runs the isolated sandbox webhook receiver until process termination.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(ctx, os.Getenv, logger); err != nil {
		logger.Error("webhook receiver stopped", "error_code", "receiver_failed")
		os.Exit(1)
	}
}

// run validates environment configuration and owns the receiver HTTP lifecycle.
func run(ctx context.Context, getenv func(string) string, logger *slog.Logger) error {
	port := strings.TrimSpace(getenv("PORT"))
	if port == "" {
		port = defaultPort
	}
	secret := []byte(getenv("IAPSTACK_WEBHOOK_RECEIVER_SECRET"))
	store, err := webhookreceiver.OpenPostgresStore(
		ctx,
		strings.TrimSpace(getenv("IAPSTACK_WEBHOOK_RECEIVER_DATABASE_URL")),
	)
	if err != nil {
		return err
	}
	receiver, err := webhookreceiver.New(webhookreceiver.Config{
		Secret: secret, BodyLimit: defaultBodyLimit,
		TimestampTolerance: defaultTimestampTolerance,
		Store:              store, Logger: logger,
	})
	for index := range secret {
		secret[index] = 0
	}
	if err != nil {
		store.Close()
		return err
	}
	defer receiver.Close()
	server := &http.Server{
		Addr: ":" + port, Handler: receiver,
		ReadHeaderTimeout: defaultReadHeaderTimeout,
		ReadTimeout:       defaultReadTimeout,
		WriteTimeout:      defaultWriteTimeout,
		IdleTimeout:       defaultIdleTimeout,
		MaxHeaderBytes:    defaultMaxHeaderBytes,
	}
	errChannel := make(chan error, 1)
	go func() {
		logger.Info("webhook receiver is ready", "address", server.Addr)
		errChannel <- server.ListenAndServe()
	}()
	select {
	case err := <-errChannel:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultShutdownTimeout)
		defer cancel()
		return server.Shutdown(shutdownContext)
	}
}
