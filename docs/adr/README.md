# Architecture Decision Records

Architecture Decision Records (ADRs) capture decisions that materially constrain IAPStack's design. They explain why a choice was made, the consequences accepted, and the alternatives considered.

## Status values

- Proposed: under review and not yet binding
- Accepted: the current decision
- Superseded: replaced by a later ADR
- Deprecated: retained for context but no longer recommended

## Index

- [ADR-0001: Begin with a Go modular monolith](0001-go-modular-monolith.md)
- [ADR-0002: Isolate stores behind a store-neutral core](0002-store-neutral-core.md)
- [ADR-0003: Use PostgreSQL as the source of truth](0003-postgresql-source-of-truth.md)
- [ADR-0004: Use API-first contracts in a monorepo](0004-api-first-monorepo.md)
- [ADR-0005: Normalize provider evidence into purchase observations](0005-provider-neutral-purchase-observations.md)
- [ADR-0006: Protect sensitive data with scoped authenticated envelopes](0006-scoped-authenticated-data-protection.md)
- [ADR-0007: Resolve provider credentials through application-scoped packages](0007-application-scoped-provider-credentials.md)
