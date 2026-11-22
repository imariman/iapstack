package persistence

import "fmt"

const (
	// ActorChannelAPI marks a write made with a bearer on the versioned HTTP API.
	ActorChannelAPI ActorChannel = "api"
	// ActorChannelDashboard marks a write made with a dashboard session.
	ActorChannelDashboard ActorChannel = "dashboard"
	// ActorChannelMCP marks a write made through the MCP endpoint.
	ActorChannelMCP ActorChannel = "mcp"
	// ActorChannelImport marks a write made by an offline import command, such as the RevenueCat import.
	ActorChannelImport ActorChannel = "import"
)

// ActorChannel names the transport an audited write arrived on, so the audit trail tells agent,
// dashboard, API, and import work apart.
type ActorChannel string

// ValidateTransportActorChannel accepts only the channels of a live transport. Manual grants and their
// revocations are never written by an offline import, so they may not record the import channel.
func ValidateTransportActorChannel(channel ActorChannel) error {
	switch channel {
	case ActorChannelAPI, ActorChannelDashboard, ActorChannelMCP:
		return nil
	default:
		return fmt.Errorf("unsupported transport actor channel %q", channel)
	}
}

// ValidateActorChannel accepts only the stored audit channels.
func ValidateActorChannel(channel ActorChannel) error {
	switch channel {
	case ActorChannelAPI, ActorChannelDashboard, ActorChannelMCP, ActorChannelImport:
		return nil
	default:
		return fmt.Errorf("unsupported actor channel %q", channel)
	}
}
