package persistence_test

import (
	"testing"

	"github.com/imariman/iapstack/internal/persistence"
)

// TestManualGrantsRecordOnlyTransportChannels verifies a manual grant and its revocation accept the api,
// dashboard, and mcp channels and never the import channel, which no grant write uses.
func TestManualGrantsRecordOnlyTransportChannels(t *testing.T) {
	t.Parallel()

	for channel, valid := range map[persistence.ActorChannel]bool{
		persistence.ActorChannelAPI: true, persistence.ActorChannelDashboard: true, persistence.ActorChannelMCP: true,
		persistence.ActorChannelImport: false, "": false, "email": false,
	} {
		if err := persistence.ValidateTransportActorChannel(channel); (err == nil) != valid {
			t.Fatalf("ValidateTransportActorChannel(%q) error = %v, want valid %t", channel, err, valid)
		}
	}
	if err := persistence.ValidateActorChannel(persistence.ActorChannelImport); err != nil {
		t.Fatalf("ValidateActorChannel(import) error = %v, want it accepted for import-written aliases", err)
	}
}

// TestCustomerAliasActorRolesAndChannels verifies alias writes accept agent actors only on the MCP channel,
// application actors only on the API channel, and administrators on any stored channel.
func TestCustomerAliasActorRolesAndChannels(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		role    persistence.APIKeyRole
		channel persistence.ActorChannel
		valid   bool
	}{
		{role: persistence.APIKeyRoleAdmin, channel: persistence.ActorChannelAPI, valid: true},
		{role: persistence.APIKeyRoleAdmin, channel: persistence.ActorChannelDashboard, valid: true},
		{role: persistence.APIKeyRoleAdmin, channel: persistence.ActorChannelMCP, valid: true},
		{role: persistence.APIKeyRoleAdmin, channel: persistence.ActorChannelImport, valid: true},
		{role: persistence.APIKeyRoleAgent, channel: persistence.ActorChannelMCP, valid: true},
		{role: persistence.APIKeyRoleAgent, channel: persistence.ActorChannelAPI},
		{role: persistence.APIKeyRoleApplication, channel: persistence.ActorChannelAPI, valid: true},
		{role: persistence.APIKeyRoleApplication, channel: persistence.ActorChannelMCP},
		{role: persistence.APIKeyRoleAdmin, channel: ""},
		{role: persistence.APIKeyRoleAdmin, channel: "email"},
		{role: "operator", channel: persistence.ActorChannelAPI},
	} {
		write := persistence.CustomerAliasWrite{
			ProjectID: "project-1", CanonicalCustomerID: "customer-1", ExternalID: "legacy-1",
			ActorRole: testCase.role, ActorKeyID: "key-1", ActorChannel: testCase.channel,
		}
		if err := write.Validate(); (err == nil) != testCase.valid {
			t.Fatalf("alias by %s over %q error = %v, want valid %t", testCase.role, testCase.channel, err, testCase.valid)
		}
	}
}
