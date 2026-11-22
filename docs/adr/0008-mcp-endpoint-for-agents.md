# ADR-0008: Serve a least-privilege MCP endpoint for AI agents

- Status: Accepted
- Date: 2026-11-22

## Context

Operators run AI agents (Claude Code, Codex, Cursor, ChatGPT, and other Model Context
Protocol clients) next to their infrastructure. Before this decision an agent could
reach IAPStack only through hand-written calls to `/v1/admin/...`. The agent had to learn
the OpenAPI contract, pagination, and error model first, and the only practical
credential was a full `admin` key, so read-only support questions ran with control-plane
write access. There was no agent-specific redaction, size limit, or audit marker.

An MCP endpoint must not become a second, looser path around the HTTP API's
authorization. Tool results are read by a language model, so customer-supplied strings
in them can carry prompt-injection text, and anything secret in them may be copied into
a transcript.

## Decision

### Transport

The API process serves MCP over Streamable HTTP at `/mcp`, built on the official Go SDK
(`github.com/modelcontextprotocol/go-sdk`, the one new direct dependency, with
`github.com/google/jsonschema-go` for schema inference and validation). It is off by
default and enabled with `IAPSTACK_MCP_ENABLED=true`. The handler is mounted through a
dedicated HTTP server option on the exact path, so the metrics route label stays
bounded and a disabled endpoint behaves like any unknown path.

The endpoint is stateless and answers each POST with one JSON body. It keeps no
sessions, opens no server-sent event streams, and returns `405` for `GET` and `DELETE`.
The server sends no server-initiated messages, and the 30-second HTTP write timeout
would cut long-lived streams anyway.

Each request is checked before any MCP work: method, then `Origin` (a browser origin
that differs from the request host gets `403`, and no CORS headers are sent), then the
bearer, then a per-key concurrency cap (`429` with `Retry-After`). The request body
limit reuses `IAPSTACK_HTTP_BODY_LIMIT`. Per-key state exists only while a key has a
request in flight.

Tools call a shared transport-neutral service, `internal/controlplane`, never loopback
HTTP. The read logic and JSON views of the HTTP handlers moved into that service, so the
HTTP API and MCP cannot drift. The same service classifies errors, so MCP tool errors
use the stable codes of the `/v1` error envelope. `/mcp` is not part of the `/v1`
OpenAPI contract; its tool catalogue is versioned in `docs/mcp.md` and pinned by a
golden contract test.

### The `agent` key role

A new API key role, `agent`, is scoped to exactly one project and has no application.
It is read-only unless created with `allow_writes: true`, which only agent keys may set.
The `api_keys` table references the project, so an agent key cannot point at a missing
project or outlive a deleted one. Agent keys authenticate only on `/mcp`; every `/v1`
route rejects them exactly like a key of the wrong role.

`/mcp` accepts stored agent keys and stored admin keys. Admin keys work but are not the
recommended path. Application keys are data-plane credentials and get `403`. The
bootstrap administrator credential has no durable key id and gets `403`. Dashboard
sessions and cookies are never consulted.

Authorization lives in the shared service. Every project-scoped operation takes an
actor (role, key id, project, write flag, and channel `api`, `dashboard`, or `mcp`) and
fails with `forbidden` before any lookup when the actor may not reach the project.
Admins reach every project, agents only their own. The service also holds the write
gate that later write tools must pass: writes require an admin or an agent key with
`allow_writes` in its own project. The channel lets later audit records tell agent,
dashboard, and API actions apart.

### Tool allowlist

Phase 1 exposes eight read-only tools, all annotated `readOnlyHint: true`, not
destructive, idempotent, and closed world, with strict input schemas and declared output
schemas: `list_projects`, `get_project_overview`, `get_subscription_metrics`,
`search_customers`, `get_customer`, `resolve_customer`, `list_financial_reports`, and
`get_setup_status`. The last one composes existing reads and one new secret-free read
of credential kinds and webhook URLs per application. It prints full store notification
URLs only from the configured `IAPSTACK_PUBLIC_URL`, never from a request `Host` header.
The OpenAPI contract is served as the resource `iapstack://openapi/v1`.

Phase 2 may add write tools for `allow_writes` keys: manual grant creation and
revocation, entitlement, product, and store product definitions, and customer aliases.
They will require `expected_revision` wherever the HTTP API does and record the `mcp`
channel on stored writes.

These stay out of MCP even with write access: provider credentials, webhook signing
secrets, API key creation and revocation, dashboard sessions, customer merges (which are
irreversible), financial report upload and supersede, and project or application
creation. Tools are a curated allowlist, never generated from the OpenAPI operations.

### Redaction

Tool output never contains raw purchase evidence, receipts, purchase tokens, signed
transactions, credential payloads, ciphertext, fingerprints, report file digests, or
webhook signing secrets. Webhook URLs lose their query string, fragment, and user
information. A test calls every tool against seeded sentinel secrets and scans every
result and every output schema for protected field names and sentinel values.

Customer-supplied strings (external ids, aliases, grant reasons) appear only as values
inside structured content, never in tool descriptions, server instructions, or error
text. Error messages are fixed text. The server instructions and documentation tell the
agent to treat result values as untrusted data. Lists are paginated with
`next_page_token`, and one result is capped at 256 KiB. Each tool call logs one line
with tool, key id, role, outcome code, and duration, never arguments or results.

## Consequences

- Agents get a least-privilege credential and a small, documented tool surface instead
  of an admin key and the full contract.
- Every change to a tool name, annotation, or schema must update the golden catalogue
  and bump the catalogue version in code and `docs/mcp.md`.
- Reverse proxies that forward an allow-list of paths must add `/mcp` explicitly.
- The read handlers of `/v1` became thin wrappers over `internal/controlplane`; new
  control-plane reads belong there first.
- Hosted chat clients that require the MCP authorization flow cannot connect yet. OAuth
  2.1 with protected-resource metadata and a consent screen needs its own ADR because it
  adds a credential type and public endpoints.

## Alternatives considered

- **Stdio subcommand that proxies to the HTTP API.** Needs no server change, but still
  requires an admin key, duplicates request mapping outside the server, and cannot serve
  hosted clients. It can be added later as a thin wrapper over the remote endpoint.
- **Let agents call `/v1` directly.** Works today but has no least-privilege credential,
  no agent-specific redaction or limits, and every agent has to rediscover the API.
- **Generate tools from every OpenAPI operation.** Rejected: it would expose credential,
  secret, and merge operations to agents by default.
- **Stateful sessions with server-sent events.** Rejected for phase 1: no tool needs
  server-initiated messages, sessions would add per-client state, and the HTTP write
  timeout would cut long streams.
