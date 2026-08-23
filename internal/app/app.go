package app

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/imariman/iapstack/internal/platform/config"
	"github.com/imariman/iapstack/internal/platform/logging"
	"github.com/imariman/iapstack/internal/transport/httpserver"
)

var (
	// ErrUsage indicates that the process mode arguments are missing or invalid.
	ErrUsage = errors.New("usage: iapstack <api|worker|migrate>")
	// ErrModeUnavailable indicates a reserved process mode that is not implemented yet.
	ErrModeUnavailable = errors.New("mode is not implemented in the current bootstrap phase")
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

	switch mode {
	case "api":
		server := httpserver.New(cfg.HTTPAddress, cfg.ShutdownTimeout, logger)
		return server.Run(ctx)
	case "worker":
		return fmt.Errorf("%w: worker", ErrModeUnavailable)
	case "migrate":
		return fmt.Errorf("%w: migrate", ErrModeUnavailable)
	default:
		panic("validated mode was not handled")
	}
}
