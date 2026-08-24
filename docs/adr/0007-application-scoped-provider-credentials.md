# ADR-0007: Resolve provider credentials through application-scoped packages

- Status: Accepted
- Date: 2026-08-24

## Context

Every purchase provider requires different authentication and verification material.
Huawei can require server API credentials and signature keys, Apple uses issuer and key
identities with private keys, Google commonly uses service-account material, and future
providers will introduce different schemas and rotation procedures.

IAPStack supports multiple projects and applications in one deployment. Global
provider-specific environment variables would prevent independent application
rotation, couple the shared process configuration to the first adapter, and require a
breaking configuration model when another provider is added.

Credentials must remain private at rest and in diagnostics. At the same time, adapters
need typed provider-owned parsing and validation; the common domain must not treat
unrelated provider fields as if they shared semantics.

## Decision

### Keep credential payloads opaque and provider-owned

`stores.Credential` contains a logical kind, media type, positive schema version, and
private payload bytes. It owns defensive copies and provides redacted string, debug,
and structured-log representations. A package can contain one atomic provider
configuration document, allowing related keys and identifiers to rotate together.

`stores.CredentialSource` resolves a package by authoritative `core.Application` and
credential kind. Concrete adapters define their own kind constants, media types,
document schemas, and typed parsers. Provider-specific credential structs never cross
the adapter boundary.

### Store only protected application-scoped values

PostgreSQL stores one current credential per `(application_id, kind)` with:

- project and application identity;
- non-secret kind, media type, and schema version;
- authenticated ciphertext, scoped fingerprint, and encryption `key_id`;
- positive optimistic revision and creation/update timestamps.

A composite application foreign key enforces project isolation. Database constraints
validate protected-value sizes, metadata shape, revisions, and timestamp order.

`internal/credentials` validates that the requested full application, including
provider, environment, and provider application ID, still matches the authoritative
catalog record. It protects writes before persistence and opens values only after this
scope check.

Protection scope authenticates project ID, application ID, provider, environment,
provider application ID, credential kind, media type, and schema version. Changing any
of these fields makes authentication fail instead of allowing the same payload to be
interpreted as another provider or schema.

### Separate credential revision from encryption-key rotation

Credential writes use an expected revision:

- revision zero creates the initial value;
- a matching positive revision rotates the payload and increments the revision;
- an identical retry returns the existing durable revision even though randomized
  encryption produced different ciphertext;
- a stale write with different logical content returns a conflict.

Logical equality uses the stable scoped keyed fingerprint plus authenticated metadata,
not ciphertext. Re-encrypting unchanged credentials under the active encryption key is
an explicit matching-revision update. It changes `key_id` and ciphertext, preserves the
fingerprint, and increments the credential revision. Old encryption keys can be removed
after every credential using them has been re-encrypted.

### Expose no plaintext through persistence or ordinary diagnostics

Persistence contracts accept and return `protection.Value`; repositories never receive
credential plaintext. The credential service returns plaintext only inside an opaque
`stores.Credential` to an explicit provider adapter. Errors may expose stable categories
and non-secret metadata but not payload, ciphertext, fingerprint, or root-key bytes.

## Consequences

- Huawei can be implemented next without adding Huawei credential fields to core,
  process configuration, or persistence interfaces.
- Apple, Google, Amazon, Samsung, and future adapters can add schemas without database
  migrations when the shared storage semantics remain sufficient.
- Each application rotates independently and concurrent administrative writes cannot
  silently overwrite one another.
- Provider adapters must validate their decoded credential schema before making network
  calls; successful decryption does not make provider-specific content valid.
- A later administrative API or deployment importer can call the same credential
  service without changing adapter-facing contracts.

## Alternatives considered

### One global environment variable set per provider

Rejected because it cannot isolate multiple applications and would make process startup
configuration provider-shaped.

### Shared columns for client IDs, secrets, public keys, and private keys

Rejected because similarly named fields have different lifecycle and validation rules
across providers and would grow with every adapter.

### Let each adapter query encrypted rows directly

Rejected because it duplicates protection, scope validation, rotation, error handling,
and redaction behavior in security-critical provider code.
