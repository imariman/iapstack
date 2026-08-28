# ADR-0006: Protect sensitive data with scoped authenticated envelopes

- Status: Accepted
- Date: 2026-08-24

## Context

Purchase evidence, provider artifacts, and provider references can contain bearer
tokens, signed receipts, and customer-linked identifiers. PostgreSQL must retain this
data for verification, reconciliation, and audit without storing it as plaintext.
The design must work for every current and future provider without placing provider
rules in cryptographic or persistence code.

Randomized encryption alone cannot supply a stable idempotency identity. A digest of
low-entropy or guessable provider data is also unsafe because an attacker could test
candidate values offline. Encryption keys must rotate without invalidating existing
records or changing their idempotency fingerprints.

## Decision

### Keep one provider-neutral protection port

`internal/protection` owns the protection scope, request, protected value, and opening
contracts. Plaintext is private inside a request and can be obtained only by an
explicit protector implementation. Verification and future reconciliation use this
port; persistence receives only ciphertext, fingerprint, and encryption `key_id`.

The scope contains project ID, application ID, and semantic purpose. Purpose examples
include purchase evidence, verified artifacts, and typed provider references. New
providers reuse these semantics rather than adding cryptographic formats.

### Use a versioned authenticated envelope

The concrete `internal/platform/protection` keyring uses AES-256-GCM through Go's
`cipher.NewGCMWithRandomNonce`. Each write receives a secure random 96-bit nonce that
is prepended by the standard-library implementation. IAPStack prepends its own
one-byte envelope version so future formats can be introduced deliberately.

Authenticated additional data is an unambiguous length-prefixed encoding of the
envelope version, format domain, project ID, application ID, purpose, and `key_id`.
Moving ciphertext to another application or purpose, changing its key identity, or
modifying any ciphertext byte therefore fails authentication. The service must rotate
a key before approaching the GCM limit of 2^32 messages under that key.

Every configured encryption root is exactly 32 bytes. HKDF-SHA-256 derives a distinct
AES key using a domain-separated label that includes `key_id`. Root material is not
retained by the immutable runtime keyring.

### Separate stable fingerprints from encryption rotation

A distinct 32-byte root derives a fingerprint key with HKDF-SHA-256. The protected
value fingerprint is HMAC-SHA-256 over a domain-separated, length-prefixed project,
application, and purpose scope followed by plaintext.

The active encryption key can change while this fingerprint root remains stable.
Identical plaintext in the same scope therefore has a deterministic fingerprint for
idempotency, while the same plaintext in another application or purpose does not.
Changing the fingerprint root is a separate migration because existing uniqueness
identities would change.

### Rotate with active writes and retained reads

The keyring has exactly one active encryption `key_id`. New values use that key. Open
selects the key named by the stored value, so retained old keys remain readable during
rotation. Removing a key before its data is re-encrypted or expired produces a stable
key-unavailable error.

Deployment secrets use these fail-fast environment contracts:

- `IAPSTACK_PROTECTION_ACTIVE_KEY_ID`
- `IAPSTACK_PROTECTION_KEY`, a strict base64-encoded 32-byte encryption root for
  initial single-key deployments, or
- `IAPSTACK_PROTECTION_KEYS`, a duplicate-free JSON object from key ID to strict
  base64-encoded 32-byte encryption root
- `IAPSTACK_PROTECTION_FINGERPRINT_KEY`, a strict base64-encoded 32-byte root

Exactly one encryption-key form is accepted. The single-key form is assigned to the
active key ID and is intended for initial managed-platform deployment; the JSON form is
required when retained read keys are needed during rotation. Missing values, malformed
JSON or base64, duplicate IDs, unknown active IDs, and wrong key lengths stop keyring
initialization. Errors may identify a variable or non-secret key ID but never include
secret material.

### Redact every normal diagnostic path

Protection requests, opening requests, protected values, configurations, and keyrings
provide redacted string, detailed debug string, and structured logging representations.
Authentication failure does not reveal whether ciphertext, scope, fingerprint, or a
known key was incorrect. Plaintext and key bytes are excluded from returned errors.

## Consequences

- Store adapters and orchestration code share one stable protection contract.
- Random nonces prevent ciphertext equality from revealing repeated evidence.
- Scoped keyed fingerprints preserve database idempotency without permitting ordinary
  unkeyed dictionary checks.
- Encryption rotation is an operational configuration change; fingerprint rotation is
  a data migration.
- Operators must retain old encryption keys long enough to read or re-encrypt their
  records and must back up both keyring configuration and the fingerprint root.
- Envelope versioning creates an explicit path for future algorithms without silently
  reinterpreting stored bytes.

## Standard-library references

- [Go `cipher.NewGCMWithRandomNonce`](https://pkg.go.dev/crypto/cipher#NewGCMWithRandomNonce)
- [Go `crypto/hkdf`](https://pkg.go.dev/crypto/hkdf)
- [Go `crypto/hmac`](https://pkg.go.dev/crypto/hmac)
