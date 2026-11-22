// Package controlplane holds transport-neutral administration use cases shared by the HTTP API and
// the MCP endpoint, so both enforce the same authorization and return the same views.
package controlplane

import (
	"errors"

	"github.com/imariman/iapstack/internal/auth"
	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
)

const (
	// ChannelAPI marks a request made with a bearer on the versioned HTTP API.
	ChannelAPI Channel = "api"
	// ChannelDashboard marks a request made with a dashboard session cookie.
	ChannelDashboard Channel = "dashboard"
	// ChannelMCP marks a request made through the MCP endpoint.
	ChannelMCP Channel = "mcp"
)

// Channel names the transport an actor used, so audit records can tell agent, dashboard, and API work apart.
type Channel string

// Actor is the authenticated caller of one control-plane operation.
type Actor struct {
	Role persistence.APIKeyRole
	// KeyID is empty for the bootstrap administrator, which has no stored key.
	KeyID         string
	ProjectID     core.ProjectID
	ApplicationID core.ApplicationID
	AllowWrites   bool
	Channel       Channel
}

var (
	// ErrForbidden means the actor is authenticated but may not perform the operation or reach the project.
	ErrForbidden = errors.New("forbidden")
)

// ActorFromPrincipal binds one authenticated principal to the transport channel it arrived on.
func ActorFromPrincipal(principal auth.Principal, channel Channel) Actor {
	return Actor{
		Role: principal.Role, KeyID: principal.KeyID, ProjectID: principal.ProjectID,
		ApplicationID: principal.ApplicationID, AllowWrites: principal.AllowWrites, Channel: channel,
	}
}

// AuthorizeProject reports whether the actor may read the control-plane data of one project.
// Administrators may read every project and an agent only its own. Application keys are
// data-plane credentials and may read no control-plane project.
func (actor Actor) AuthorizeProject(projectID core.ProjectID) error {
	switch actor.Role {
	case persistence.APIKeyRoleAdmin:
		return nil
	case persistence.APIKeyRoleAgent:
		if actor.ProjectID != "" && actor.ProjectID == projectID {
			return nil
		}
		return ErrForbidden
	default:
		return ErrForbidden
	}
}

// AuthorizeWrite reports whether the actor may change control-plane state in one project.
// Administrators may write everywhere. An agent may write only in its own project and only when its
// key was created with allow_writes. Every other actor is read-only.
func (actor Actor) AuthorizeWrite(projectID core.ProjectID) error {
	if err := actor.AuthorizeProject(projectID); err != nil {
		return err
	}
	switch actor.Role {
	case persistence.APIKeyRoleAdmin:
		return nil
	case persistence.APIKeyRoleAgent:
		if actor.AllowWrites {
			return nil
		}
		return ErrForbidden
	default:
		return ErrForbidden
	}
}

// authorizeApplicationProject lets an application key reach its own project on a data-plane route,
// and otherwise applies the control-plane project rule.
func (actor Actor) authorizeApplicationProject(projectID core.ProjectID) error {
	if actor.Role == persistence.APIKeyRoleApplication {
		if actor.ProjectID != "" && actor.ProjectID == projectID && actor.ApplicationID != "" {
			return nil
		}
		return ErrForbidden
	}
	return actor.AuthorizeProject(projectID)
}
