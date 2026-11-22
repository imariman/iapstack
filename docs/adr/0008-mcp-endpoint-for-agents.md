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
actor (role, key id, durable audit identity, project, write flag, and channel `api`,
`dashboard`, or `mcp`) and fails with `forbidden` before any lookup when the actor may
not reach the project. Admins reach every project, agents only their own. Every write
passes the service's write gate before validation or storage: it requires an admin or
an agent key with `allow_writes` in its own project. The channel lets audit records
tell agent, dashboard, and API actions apart.

### Tool allowlist

Phase 1 exposes eight read-only tools, all annotated `readOnlyHint: true`, not
destructive, idempotent, and closed world, with strict input schemas and declared output
schemas: `list_projects`, `get_project_overview`, `get_subscription_metrics`,
`search_customers`, `get_customer`, `resolve_customer`, `list_financial_reports`, and
`get_setup_status`. The last one has no `/v1` counterpart. It uses one narrow,
secret-free read of applications, stored credential kinds, webhook presence, queue
outcomes, and recent deliveries, not the dashboard overview. It prints full store
notification URLs only from the configured `IAPSTACK_PUBLIC_URL`, never from a request
`Host` header, and reports an application whose provider has no adapter as
`provider_not_supported`.
The OpenAPI contract is served as the resource `iapstack://openapi/v1`.

Phase 2 adds six write tools: `create_manual_grant`, `revoke_manual_grant`,
`put_entitlement`, `put_product`, `put_store_product`, and `put_customer_alias`. See
[Write tools](#write-tools-phase-2).

These stay out of MCP even with write access: provider credentials, webhook signing
secrets, API key creation and revocation, dashboard sessions, customer merges (which are
irreversible), financial report upload and supersede, and project or application
creation. Tools are a curated allowlist, never generated from the OpenAPI operations.

### Redaction

Tool output never contains raw purchase evidence, receipts, purchase tokens, signed
transactions, credential payloads, ciphertext, fingerprints, report file digests, or
webhook signing secrets. A stored webhook URL is reduced to its origin (scheme and
host): the path, query string, fragment, and user information can each carry a token,
and no `/v1` read route returns the URL either. A test calls every tool against seeded
sentinel secrets and scans every result and every output schema for protected field
names and sentinel values.

Customer-supplied strings (external ids, aliases, grant reasons) appear only as values
inside structured content, never in tool descriptions, server instructions, or error
text. Error messages are fixed text. The server instructions and documentation tell the
agent to treat result values as untrusted data. Lists are paginated with a
`next_page_token` that names the last item read rather than an offset, and one
serialized result, text and structured content together, is capped at 256 KiB. Each
tool call logs one line with tool, key id, role, channel, whether the tool writes,
outcome code, and duration. A write call also logs its project id. Other arguments and
results are never logged.

### Write tools (phase 2)

**Shared write services.** The six writes moved out of the `/v1` handlers into
`internal/controlplane`, the same way the reads did: `CreateManualGrant`,
`RevokeManualGrant`, `PutCustomerAlias`, `PutEntitlement`, `PutProduct`, and
`PutStoreProduct` take the actor, authorize first, then validate, then reach storage.
The `/v1` handlers and the MCP tools both call them, so validation, idempotency, and
error codes cannot drift; `/v1` responses only gained the audit channel fields. The
bootstrap administrator, which has no stored key or account, may not make the audited
writes (grants, revocations, aliases) on either transport. The data-plane alias route
keeps its rule that an application key may attach aliases in its own project.

**No `expected_revision`.** None of these six writes takes an expected revision on
`/v1`, so none takes one on MCP. Their own replay rules protect them instead: a grant
requires an idempotency key, a repeated revocation returns the stored one, and the four
put operations create a record or confirm an identical one and never overwrite a
different one (`conflict`). Adding revisions would have changed the `/v1` contract.

**Annotations.** Every write tool is `readOnlyHint: false`, `openWorldHint: false`, and
`idempotentHint: true`, because a repeat with the same arguments has no further effect.
Only `revoke_manual_grant` is `destructiveHint: true`: it removes access and cannot be
undone. The put tools are not destructive because they never overwrite. `docs/mcp.md`
gives the reason per tool.

**Least-privilege tool listing.** The handler builds two MCP servers, one with the read
tools and one with all tools, and picks one per request from the authenticated actor. A
key that cannot write never sees the write tools. The listing is not the boundary: the
service's write gate runs on every call, and tests cover both layers.

**Audit channel.** `customer_aliases` and `manual_grants` gained a stored channel:
`actor_channel` on both and `revoked_by_channel` for a revocation, each checked against
`api`, `dashboard`, `mcp`, and `import`. `import` marks aliases written by the offline
RevenueCat import, which has no transport channel. Aliases also accept the role
`agent`, and a check ties roles to channels: an agent alias is always `mcp` and an
application alias always `api`. `/v1` records `dashboard` when a dashboard session
authenticated the request and `api` for a bearer. The customer detail on `/v1` and
`get_customer` return the channel on aliases, manual grants, and timeline events.

**Definition writes are audited by log line only.** Entitlement, product, and store
product definitions have no stored audit row on any transport, and this phase does not
add one. An MCP definition write is recorded by the tool call's log line, which names
the tool, key, role, channel, project, and outcome. The definition itself is
create-once: a later write can only confirm it. A stored audit trail for definitions,
if needed, is a separate decision.

## Consequences

- Agents get a least-privilege credential and a small, documented tool surface instead
  of an admin key and the full contract.
- Every change to a tool name, annotation, or schema must update the golden catalogue
  and bump the catalogue version in code and `docs/mcp.md`.
- Reverse proxies that forward an allow-list of paths must add `/mcp` explicitly.
- The read handlers and the six shared write handlers of `/v1` became thin wrappers
  over `internal/controlplane`; new control-plane operations belong there first.
- Agent writes are attributable: aliases and manual grants carry the agent key id and
  the `mcp` channel. Definition writes are visible only in logs, so operators who need a
  durable record of agent definition writes must keep the MCP log lines.
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
