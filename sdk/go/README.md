# IAPStack Go host SDK

Trusted-host client for the IAPStack v1 API. This package mints short-lived
customer sessions with the durable application bearer, looks up entitlements on
the customer-session path declared by the public contract, and verifies signed
outbound webhooks.

Authenticate the user in your backend first. Then mint a customer session here
and return **only** that token to a mobile client. Never ship the durable
application bearer in a Flutter, iOS, Android, or React Native binary, and never
log or persist either bearer.

This is the first post-v0.1 SDK. Flutter remains the only v0.1 mobile client.
The package is not published to a module proxy yet; depend on the repository
path `sdk/go`.

## Trusted-host boundary

| Operation | Auth | Who calls it |
| --- | --- | --- |
| `POST /v1/applications/{application_id}/customer-sessions` | Durable application bearer | This SDK, after the host authenticates its user |
| `GET /v1/applications/{application_id}/customers/{external_customer_id}/entitlements` | Customer session bearer | This SDK using a token it just minted, or a mobile client |
| Outbound webhook `IAPStack-Signature` | HMAC-SHA-256 `v2` | This SDK on your webhook HTTP handler |

The public OpenAPI contract does **not** allow the application bearer on
entitlement GET. The supported host lookup path is: mint a customer session,
then call `GetEntitlements` with that token, or consume signed
`entitlement.changed` webhooks. Do not send the application bearer on the GET.

## Usage

```go
package main

import (
	"context"
	"log"
	"net/http"
	"os"

	iapstack "github.com/imariman/iapstack/sdk/go"
)

func mintSession(w http.ResponseWriter, r *http.Request) {
	client, err := iapstack.NewClient(iapstack.Config{
		BaseURL:          "https://iap.example.com",
		ApplicationID:    "my-application",
		ApplicationToken: os.Getenv("IAPSTACK_APPLICATION_KEY"),
	})
	if err != nil {
		http.Error(w, "unavailable", http.StatusInternalServerError)
		return
	}
	defer client.Close()

	session, err := client.CreateCustomerSession(r.Context(), "customer-123")
	if err != nil {
		http.Error(w, "unavailable", http.StatusInternalServerError)
		return
	}

	snapshot, err := client.GetEntitlements(r.Context(), "customer-123", session.Token)
	if err != nil {
		http.Error(w, "unavailable", http.StatusInternalServerError)
		return
	}
	hasPremium := false
	for _, entitlement := range snapshot.Entitlements {
		if entitlement.Key == "premium" && entitlement.GrantsAccess() {
			hasPremium = true
			break
		}
	}
	_ = hasPremium

	// Return only the short-lived customer token to the mobile app.
	_, _ = w.Write([]byte(session.Token))
}

func webhook(w http.ResponseWriter, r *http.Request) {
	verifier, err := iapstack.NewWebhookVerifier(iapstack.WebhookConfig{
		Secret: []byte(os.Getenv("IAPSTACK_WEBHOOK_SECRET")),
		Store:  iapstack.NewMemoryEventStore(),
	})
	if err != nil {
		http.Error(w, "unavailable", http.StatusInternalServerError)
		return
	}
	defer verifier.Close()

	event, err := verifier.VerifyRequest(r)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if event.Duplicate {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	log.Printf("entitlement %s access=%s", event.Change.EntitlementKey, event.Change.Access)
	w.WriteHeader(http.StatusNoContent)
}
```

Replace `NewMemoryEventStore` with a durable store in production so retries
across processes still dedupe by the authenticated `IAPStack-Event-ID`.

## Webhook verification

Verify `IAPStack-Signature` as `v2=<hex>` HMAC-SHA-256 over:

```text
v2
<event_id>
<unix_timestamp>
<exact_raw_body>
```

The verifier rejects `v1` signatures, timestamps outside the five-minute replay
window, event IDs that do not match the MAC, and conflicting bodies for a reused
event ID.

## Testing

```sh
cd sdk/go
gofmt -l .
go vet ./...
go test -race -count=1 ./...
```
