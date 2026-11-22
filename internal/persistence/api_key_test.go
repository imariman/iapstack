package persistence_test

import (
	"testing"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
)

// TestValidateAPIKeyScopeAcceptsAgentProjectScope verifies the agent role scope and the write flag rules.
func TestValidateAPIKeyScopeAcceptsAgentProjectScope(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name          string
		role          persistence.APIKeyRole
		projectID     core.ProjectID
		applicationID core.ApplicationID
		allowWrites   bool
		valid         bool
	}{
		{name: "admin", role: persistence.APIKeyRoleAdmin, valid: true},
		{name: "admin with writes flag", role: persistence.APIKeyRoleAdmin, allowWrites: true},
		{name: "application", role: persistence.APIKeyRoleApplication, projectID: "project-1", applicationID: "application-1", valid: true},
		{name: "application with writes flag", role: persistence.APIKeyRoleApplication, projectID: "project-1", applicationID: "application-1", allowWrites: true},
		{name: "read-only agent", role: persistence.APIKeyRoleAgent, projectID: "project-1", valid: true},
		{name: "writing agent", role: persistence.APIKeyRoleAgent, projectID: "project-1", allowWrites: true, valid: true},
		{name: "agent without project", role: persistence.APIKeyRoleAgent},
		{name: "agent with application", role: persistence.APIKeyRoleAgent, projectID: "project-1", applicationID: "application-1"},
		{name: "agent with invalid project", role: persistence.APIKeyRoleAgent, projectID: " project-1"},
		{name: "unknown role", role: "operator", projectID: "project-1"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			err := persistence.ValidateAPIKeyScope(testCase.role, testCase.projectID, testCase.applicationID, testCase.allowWrites)
			if (err == nil) != testCase.valid {
				t.Fatalf("ValidateAPIKeyScope() error = %v, want valid %t", err, testCase.valid)
			}
		})
	}
}
