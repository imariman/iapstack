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
then call `GetEntitlements` with that session, or consume signed
`entitlement.changed` webhooks. Do not send the application bearer on the GET.

## Usage

Construct the HTTP client and webhook verifier once at process start. IAPStack
retries webhook delivery only for 408, 429, and 5xx. Mapping every verification
failure to 401 marks the event `webhook_rejected` permanently. Building a new
memory store inside the handler also makes `Duplicate` false on every retry.

```go
package main

import (
	"context"
	"log"
	"net/http"
	"os"

	iapstack "github.com/imariman/iapstack/sdk/go"
)

func main() {
	client, err := iapstack.NewClient(iapstack.Config{
		BaseURL:          "https://iap.example.com",
		ApplicationID:    "my-application",
		ApplicationToken: os.Getenv("IAPSTACK_APPLICATION_KEY"),
	})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	verifier, err := iapstack.NewWebhookVerifier(iapstack.WebhookConfig{
		Secret: []byte(os.Getenv("IAPSTACK_WEBHOOK_SECRET")),
		Store:  iapstack.NewMemoryEventStore(),
	})
	if err != nil {
		log.Fatal(err)
	}
	defer verifier.Close()

	http.HandleFunc("/session", func(writer http.ResponseWriter, request *http.Request) {
		session, err := client.CreateCustomerSession(request.Context(), "customer-123")
		if err != nil {
			http.Error(writer, "unavailable", http.StatusInternalServerError)
			return
		}

		snapshot, err := client.GetEntitlements(request.Context(), session)
		if err != nil {
			http.Error(writer, "unavailable", http.StatusInternalServerError)
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
		_, _ = writer.Write([]byte(session.Token))
	})

	http.Handle("/webhooks/iapstack", verifier.Handler(func(_ context.Context, event iapstack.WebhookEvent) error {
		log.Printf("entitlement %s access=%s", event.Change.EntitlementKey, event.Change.Access)
		return nil
	}))

	log.Fatal(http.ListenAndServe(":8080", nil))
}
```

Replace `NewMemoryEventStore` with a durable store in production so retries
across processes still dedupe by the authenticated `IAPStack-Event-ID`. The
handler hashes the raw body and passes an opaque fingerprint into `Remember`;
do not recompute SHA-256 in the store.

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
event ID. `Handler` maps those failures to 401, identity conflict to 409, and
store or host-callback errors to 503.

## Testing

```sh
cd sdk/go
gofmt -l .
go vet ./...
go test -race -count=1 ./...
```
