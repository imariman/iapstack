package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/imariman/iapstack/internal/app"
)

// main configures process signal handling and runs the selected IAPStack mode.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := app.Run(ctx, os.Args[1:], os.Getenv, os.Stdout); err != nil {
		logProcessFailure(os.Stderr, err)
		os.Exit(1)
	}
}

// logProcessFailure emits a stable diagnostic code without serializing error chains
// that can contain connection strings, provider responses, or other secret material.
func logProcessFailure(output io.Writer, err error) {
	errorCode := "process_failed"
	if errors.Is(err, app.ErrUsage) {
		errorCode = "invalid_usage"
	}
	slog.New(slog.NewJSONHandler(output, nil)).Error("iapstack stopped", "error_code", errorCode)
}
