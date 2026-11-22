package app

import (
	"io"
	"log/slog"
	"testing"

	"github.com/imariman/iapstack/internal/auth"
	"github.com/imariman/iapstack/internal/controlplane"
	"github.com/imariman/iapstack/internal/persistence"
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

// TestMCPControlPlaneDependenciesStayNilWithoutServices verifies absent services reach the control plane as
// nil interfaces, so its nil checks answer with an error instead of dereferencing a typed nil pointer.
func TestMCPControlPlaneDependenciesStayNilWithoutServices(t *testing.T) {
	dependencies := mcpControlPlaneDependencies(config.Config{PublicURL: "https://iap.example.com"}, nil, nil, nil)
	if dependencies.Store != nil || dependencies.Operations != nil || dependencies.Admin != nil || dependencies.Setup != nil ||
		dependencies.Protection != nil || dependencies.FinancialReports != nil {
		t.Fatalf("dependencies = %#v, want nil interfaces for absent services", dependencies)
	}
	if dependencies.PublicURL != "https://iap.example.com" {
		t.Fatalf("PublicURL = %q, want the configured URL", dependencies.PublicURL)
	}
	service := controlplane.New(dependencies)
	admin := controlplane.Actor{Role: persistence.APIKeyRoleAdmin, KeyID: "admin-key"}
	if _, err := service.FinancialReports(t.Context(), admin, "project-1"); err == nil {
		t.Fatal("FinancialReports() error = nil, want a missing dependency error")
	}
	if _, err := service.SearchCustomers(t.Context(), admin, controlplane.CustomerSearchInput{
		ProjectID: "project-1", Query: "customer-1",
	}); err == nil {
		t.Fatal("SearchCustomers() error = nil, want a missing dependency error")
	}
	if err := service.PutEntitlement(t.Context(), admin, controlplane.EntitlementInput{
		ProjectID: "project-1", EntitlementID: "entitlement-1", Key: "premium",
	}); err == nil {
		t.Fatal("PutEntitlement() error = nil, want a missing dependency error")
	}
}
