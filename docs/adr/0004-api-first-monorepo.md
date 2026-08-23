# ADR-0004: Use API-first contracts in a monorepo

- Status: Accepted
- Date: 2026-08-23

## Context

IAPStack includes a Go server, dashboard, Flutter SDK, later client SDKs, webhook consumers, deployment templates, and documentation. These components must evolve together without relying on undocumented server behavior.

Splitting them into repositories immediately would add cross-repository release coordination before the public contracts stabilize.

## Decision

IAPStack will use one repository for the server, dashboard, SDKs, public contracts, deployment assets, and documentation.

Versioned API and webhook schemas under `contracts/` are the compatibility boundary. The dashboard and first-party SDKs consume the same public contracts available to third-party integrations. They must not depend on database access or private Go implementation details.

Breaking contract changes require an explicit version transition and migration notes. Internal package refactors do not require public version changes when observable contracts remain compatible.

## Consequences

- One pull request can update a contract, server behavior, SDK, dashboard, tests, and documentation atomically.
- CI can run cross-component compatibility tests before merge.
- Repository tooling must avoid running every expensive job for unrelated changes as the project grows.
- Independent package release versions may still be needed for SDK distribution.
- A component may move to another repository later if ownership or release cadence clearly diverges; public contracts remain the integration boundary.

## Alternatives considered

### One repository per component

Rejected initially because it increases coordination and makes contract drift easier during early development.

### Let each client mirror server behavior manually

Rejected because it creates inconsistent validation, error handling, and release timing across SDKs.
