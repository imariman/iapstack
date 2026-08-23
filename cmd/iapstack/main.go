package main

import (
	"context"
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
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("iapstack stopped", "error", err)
		os.Exit(1)
	}
}
