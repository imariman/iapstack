# IAPStack TypeScript host SDK

Trusted-host client for the IAPStack v1 API. This package mints short-lived
customer sessions with the durable application bearer, looks up entitlements on
the customer-session path declared by the public contract, and verifies signed
outbound webhooks.

Authenticate the user in your backend first. Then mint a customer session here
and return **only** that token to a mobile client. Never ship the durable
application bearer in a Flutter, iOS, Android, React Native, or browser
bundle, and never log or persist either bearer.

This is a post-v0.1 host SDK for Node backends (Express, Nest, Next.js Route
Handlers). Flutter remains the only v0.1 mobile client. Browser usage is not
supported. The package is not published to npm yet; depend on the repository
path `sdk/typescript`.

## Trusted-host boundary

| Operation | Auth | Who calls it |
| --- | --- | --- |
| `POST /v1/applications/{application_id}/customer-sessions` | Durable application bearer | This SDK, after the host authenticates its user |
| `GET /v1/applications/{application_id}/customers/{external_customer_id}/entitlements` | Customer session bearer | This SDK using a token it just minted, or a mobile client |
| Outbound webhook `IAPStack-Signature` | HMAC-SHA-256 `v2` | This SDK on your webhook HTTP handler |

The public OpenAPI contract does **not** allow the application bearer on
entitlement GET. The supported host lookup path is: mint a customer session,
then call `getEntitlements` with that session, or consume signed
`entitlement.changed` webhooks. Do not send the application bearer on the GET.

## Usage

Construct the HTTP client and webhook verifier once at process start. IAPStack
retries webhook delivery only for 408, 429, and 5xx. Mapping every verification
failure to 401 marks the event `webhook_rejected` permanently. Building a new
memory store inside the handler also makes `duplicate` false on every retry.

```ts
import {
  Client,
  MemoryEventStore,
  WebhookVerifier,
} from '@iapstack/host';

const client = new Client({
  baseUrl: 'https://iap.example.com',
  applicationId: 'my-application',
  applicationToken: process.env.IAPSTACK_APPLICATION_KEY ?? '',
});

const verifier = new WebhookVerifier({
  secret: process.env.IAPSTACK_WEBHOOK_SECRET ?? '',
  store: new MemoryEventStore(),
});

async function mintSession(externalCustomerId: string): Promise<string> {
  const session = await client.createCustomerSession(externalCustomerId);
  const snapshot = await client.getEntitlements(session);
  const hasPremium = snapshot.entitlements.some(
    (item) => item.key === 'premium' && item.grantsAccess(),
  );
  void hasPremium;

  // Return only the short-lived customer token to the mobile app.
  return session.token;
}

const handleWebhook = verifier.handler(async (event) => {
  console.log(`entitlement ${event.change.entitlementKey} access=${event.change.access}`);
});

export async function POST(request: Request): Promise<Response> {
  return handleWebhook(request);
}

void mintSession;
```

Next.js App Router handlers can pass the Fetch `Request` directly. Node `http`
and Express hosts can pass the exact raw body as a `WebhookRequest` into the
same `handler`. Pass the exact raw webhook body; do not re-serialize parsed JSON.
`handler` maps signature and timestamp failures to 401, identity conflict to
409, and store or host-callback errors to 503.

Replace `MemoryEventStore` with a durable store in production so retries
across processes still dedupe by the authenticated `IAPStack-Event-ID`. The
handler hashes the raw body and passes an opaque fingerprint into `remember`;
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
event ID. `handler` maps those failures to 401, identity conflict to 409, and
store or host-callback errors to 503.

## Testing

```sh
cd sdk/typescript
bun install --frozen-lockfile
bun run typecheck
bun test
```
