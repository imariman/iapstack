package app

import (
	"io"
	"log/slog"
	"testing"

	"github.com/imariman/iapstack/internal/auth"
	"github.com/imariman/iapstack/internal/persistence/postgres"
	"github.com/imariman/iapstack/internal/platform/config"
)

// TestNewMCPHandlerIsOptIn verifies the MCP endpoint exists only when IAPSTACK_MCP_ENABLED is true.
func TestNewMCPHandlerIsOptIn(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var store *postgres.Store
	authentication, err := auth.NewService(store, "", 1)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	disabled, err := newMCPHandler(config.Config{HTTPBodyLimit: 1024, MCPMaxConcurrentRequestsPerKey: 4}, store, nil, authentication, nil, logger)
	if err != nil || disabled != nil {
		t.Fatalf("newMCPHandler() disabled = %v, %v, want no handler", disabled, err)
	}
	enabled, err := newMCPHandler(config.Config{
		HTTPBodyLimit: 1024, MCPEnabled: true, MCPMaxConcurrentRequestsPerKey: 4,
	}, store, nil, authentication, nil, logger)
	if err != nil || enabled == nil {
		t.Fatalf("newMCPHandler() enabled = %v, %v, want a handler", enabled, err)
	}
}
