// Package iapstack is the trusted-host SDK for IAPStack v1.
//
// Host backends authenticate their own users, mint short-lived customer
// sessions with a durable application bearer, look up entitlements on the
// customer-session path declared by the public contract, and verify signed
// outbound webhooks. This package consumes contracts/openapi/v1.yaml and the
// webhook MAC contract. It does not import server internals or talk to
// PostgreSQL.
//
// The durable application bearer must never ship in a mobile binary.
package iapstack
