# ADR-0001: Begin with a Go modular monolith

- Status: Accepted
- Date: 2026-08-23

## Context

IAPStack needs consistent transactions across purchase evidence, entitlement projections, notification processing, and outbound webhooks. The first maintainers must also be able to run, test, and deploy the complete backend without operating a distributed system.

The architecture must still allow API traffic and asynchronous work to scale independently when necessary.

## Decision

The initial server will be one Go module organized as a modular monolith. Domain boundaries are enforced through Go packages and dependency direction rather than network calls.

One build artifact will expose explicit API, worker, and migration modes. API and worker processes may be deployed and scaled separately while sharing the same versioned code and PostgreSQL database.

The core domain will not import HTTP handlers, concrete store adapters, or PostgreSQL implementations.

## Consequences

- Cross-domain invariants can use local database transactions.
- Local development and self-hosted deployment remain straightforward.
- API and worker deployments can scale independently without premature service decomposition.
- Package boundaries require active review because the compiler cannot prevent every architectural shortcut.
- A future service extraction must introduce an explicit contract and preserve idempotency semantics.

## Alternatives considered

### Microservices from the first release

Rejected because they add deployment, tracing, failure-mode, and contract-versioning cost before independent scaling boundaries are known.

### A single undifferentiated package

Rejected because store-specific and transport concerns would quickly leak into the entitlement domain and make additional store adapters unsafe.
