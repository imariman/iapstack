import { test } from 'bun:test';
import assert from 'node:assert/strict';
import { createHash, createHmac } from 'node:crypto';
import { webhookStatus } from '../src/errors';
import {
  MemoryEventStore,
  WebhookError,
  WebhookVerifier,
  type EventStore,
  type WebhookRequest,
} from '../src/index';

const testWebhookSecret = '0123456789abcdef0123456789abcdef';
const testWebhookBody =
  '{' +
  '"schema_version":1,' +
  '"project_id":"project-1",' +
  '"application_id":"ios-sandbox",' +
  '"customer_id":"customer-internal",' +
  '"entitlement_id":"entitlement-1",' +
  '"entitlement_key":"premium",' +
  '"access":"allowed",' +
  '"access_reason":"purchase_valid",' +
  '"source_observation_id":"observation-1",' +
  '"source_application_id":"ios-sandbox",' +
  '"source_product_id":"premium_annual",' +
  '"version":1' +
  '}';

const defaultWebhookBodyLimit = 1 << 20;

class FailingStore implements EventStore {
  constructor(private readonly error: unknown) {}

  remember(): never {
    throw this.error;
  }
}

function webhookCode(error: unknown, code: string): boolean {
  return error instanceof WebhookError && error.code === code && error.statusCode === webhookStatus(code);
}

function signWebhook(eventId: string, timestamp: Date, body: Uint8Array | string): string {
  const mac = createHmac('sha256', testWebhookSecret);
  mac.update('v2');
  mac.update('\n');
  mac.update(eventId);
  mac.update('\n');
  mac.update(String(Math.floor(timestamp.getTime() / 1000)));
  mac.update('\n');
  mac.update(body);
  return mac.digest('hex');
}

function signedWebhookRequest(
  timestamp: Date,
  body: Uint8Array | string,
  eventId: string,
): WebhookRequest {
  const payload = typeof body === 'string' ? Buffer.from(body) : body;
  return {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'IAPStack-Event-ID': eventId,
      'IAPStack-Timestamp': String(Math.floor(timestamp.getTime() / 1000)),
      'IAPStack-Signature': `v2=${signWebhook(eventId, timestamp, payload)}`,
    },
    body: payload,
  };
}

function newTestVerifier(
  now: Date,
  bodyLimit = defaultWebhookBodyLimit,
  store = new MemoryEventStore(),
): WebhookVerifier {
  return new WebhookVerifier({
    secret: testWebhookSecret,
    bodyLimit,
    timestampToleranceMs: 5 * 60 * 1000,
    store,
    clock: () => now,
  });
}

test('webhook verifier accepts an authenticated event', async () => {
  const now = new Date('2026-08-31T10:00:00.000Z');
  const verifier = newTestVerifier(now);
  const event = await verifier.verifyRequest(signedWebhookRequest(now, testWebhookBody, 'event-1'));
  assert.equal(event.id, 'event-1');
  assert.equal(event.duplicate, false);
  assert.equal(event.change.entitlementKey, 'premium');
  assert.equal(event.change.entitlement().grantsAccessAt(now), true);
  verifier.close();
});

test('webhook verifier rejects an invalid signature before storage', async () => {
  const now = new Date('2026-08-31T10:00:00.000Z');
  const store = new MemoryEventStore();
  const verifier = newTestVerifier(now, defaultWebhookBodyLimit, store);
  const request = signedWebhookRequest(now, testWebhookBody, 'event-1');
  request.headers = {
    ...request.headers,
    'IAPStack-Signature': `v2=${'00'.repeat(32)}`,
  };
  await assert.rejects(
    () => verifier.verifyRequest(request),
    (error) => webhookCode(error, 'invalid_signature'),
  );
  assert.equal(store.remember('event-1', createHash('sha256').update(testWebhookBody).digest()), false);
  verifier.close();
});

test('webhook verifier rejects a stale timestamp', async () => {
  const now = new Date('2026-08-31T10:00:00.000Z');
  const verifier = newTestVerifier(now);
  const request = signedWebhookRequest(
    new Date(now.getTime() - 6 * 60 * 1000),
    testWebhookBody,
    'event-1',
  );
  await assert.rejects(
    () => verifier.verifyRequest(request),
    (error) => webhookCode(error, 'invalid_timestamp'),
  );
  verifier.close();
});

test('webhook verifier rejects a future timestamp outside the window', async () => {
  const now = new Date('2026-08-31T10:00:00.000Z');
  const verifier = newTestVerifier(now);
  const request = signedWebhookRequest(
    new Date(now.getTime() + 6 * 60 * 1000),
    testWebhookBody,
    'event-1',
  );
  await assert.rejects(
    () => verifier.verifyRequest(request),
    (error) => webhookCode(error, 'invalid_timestamp'),
  );
  verifier.close();
});

test('webhook verifier rejects event-ID substitution', async () => {
  const now = new Date('2026-08-31T10:00:00.000Z');
  const verifier = newTestVerifier(now);
  const request = signedWebhookRequest(now, testWebhookBody, 'event-1');
  request.headers = { ...request.headers, 'IAPStack-Event-ID': 'attacker-new-event' };
  await assert.rejects(
    () => verifier.verifyRequest(request),
    (error) => webhookCode(error, 'invalid_signature'),
  );
  verifier.close();
});

test('webhook verifier rejects legacy v1 signatures', async () => {
  const now = new Date('2026-08-31T10:00:00.000Z');
  const verifier = newTestVerifier(now);
  const timestampText = String(Math.floor(now.getTime() / 1000));
  const mac = createHmac('sha256', testWebhookSecret);
  mac.update(`${timestampText}.`);
  mac.update(testWebhookBody);
  await assert.rejects(
    () =>
      verifier.verifyRequest({
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'IAPStack-Event-ID': 'attacker-new-event',
          'IAPStack-Timestamp': timestampText,
          'IAPStack-Signature': `v1=${mac.digest('hex')}`,
        },
        body: testWebhookBody,
      }),
    (error) => webhookCode(error, 'invalid_signature'),
  );
  verifier.close();
});

test('webhook verifier deduplicates an exact authenticated replay', async () => {
  const now = new Date('2026-08-31T10:00:00.000Z');
  const verifier = newTestVerifier(now);
  const first = await verifier.verifyRequest(signedWebhookRequest(now, testWebhookBody, 'event-1'));
  const second = await verifier.verifyRequest(signedWebhookRequest(now, testWebhookBody, 'event-1'));
  assert.equal(first.duplicate, false);
  assert.equal(second.duplicate, true);
  verifier.close();
});

test('webhook verifier rejects an event identity conflict', async () => {
  const now = new Date('2026-08-31T10:00:00.000Z');
  const verifier = newTestVerifier(now);
  await verifier.verifyRequest(signedWebhookRequest(now, testWebhookBody, 'event-1'));
  const altered = testWebhookBody.replace('"version":1', '"version":2');
  await assert.rejects(
    () => verifier.verifyRequest(signedWebhookRequest(now, altered, 'event-1')),
    (error) => webhookCode(error, 'event_identity_conflict'),
  );
  verifier.close();
});

test('webhook verifier bounds the webhook body', async () => {
  const now = new Date('2026-08-31T10:00:00.000Z');
  const verifier = newTestVerifier(now, 16);
  const body = 'a'.repeat(17);
  await assert.rejects(
    () => verifier.verifyRequest(signedWebhookRequest(now, body, 'event-1')),
    (error) => webhookCode(error, 'body_too_large'),
  );
  verifier.close();
});

test('NewWebhookVerifier rejects a short secret', () => {
  assert.throws(
    () => new WebhookVerifier({ secret: 'short', store: new MemoryEventStore() }),
  );
});

test('webhook verifier rejects method and content type', async () => {
  const now = new Date('2026-08-31T10:00:00.000Z');
  const verifier = newTestVerifier(now);
  await assert.rejects(
    () =>
      verifier.verifyRequest({
        method: 'GET',
        headers: { 'Content-Type': 'application/json' },
        body: testWebhookBody,
      }),
    (error) => webhookCode(error, 'method_not_allowed'),
  );
  const request = signedWebhookRequest(now, testWebhookBody, 'event-1');
  request.headers = { ...request.headers, 'Content-Type': 'text/plain' };
  await assert.rejects(
    () => verifier.verifyRequest(request),
    (error) => webhookCode(error, 'content_type_required'),
  );
  verifier.close();
});

test('webhook verifier rejects invalid event IDs and payloads', async () => {
  const now = new Date('2026-08-31T10:00:00.000Z');
  const verifier = newTestVerifier(now);
  await assert.rejects(
    () => verifier.verifyRequest(signedWebhookRequest(now, testWebhookBody, 'event 1')),
    (error) => webhookCode(error, 'invalid_event_id'),
  );
  await assert.rejects(
    () => verifier.verifyRequest(signedWebhookRequest(now, '{"schema_version":1}', 'event-1')),
    (error) => webhookCode(error, 'invalid_event'),
  );
  verifier.close();
});

test('webhook verifier applies default bounds', async () => {
  const now = new Date('2026-08-31T10:00:00.000Z');
  const verifier = new WebhookVerifier({
    secret: testWebhookSecret,
    store: new MemoryEventStore(),
    clock: () => now,
  });
  await verifier.verifyRequest(signedWebhookRequest(now, testWebhookBody, 'event-1'));
  verifier.close();
});

test('webhook verifier rejects a missing request', async () => {
  const verifier = newTestVerifier(new Date());
  await assert.rejects(
    () => verifier.verifyRequest(null),
    (error) => webhookCode(error, 'invalid_body'),
  );
  verifier.close();
});

test('webhook verifier accepts Node lowercase headers and JSON charset', async () => {
  const now = new Date('2026-08-31T10:00:00.000Z');
  const verifier = newTestVerifier(now);
  const eventId = 'event-node';
  const body = Buffer.from(testWebhookBody);
  const event = await verifier.verifyRequest({
    method: 'POST',
    headers: {
      'content-type': 'application/json; charset=utf-8',
      'iapstack-event-id': eventId,
      'iapstack-timestamp': String(Math.floor(now.getTime() / 1000)),
      'iapstack-signature': `v2=${signWebhook(eventId, now, body)}`,
    },
    body,
  });
  assert.equal(event.id, eventId);
  verifier.close();
});

test('webhook verifier accepts a Fetch Request', async () => {
  const now = new Date('2026-08-31T10:00:00.000Z');
  const verifier = newTestVerifier(now);
  const unsigned = signedWebhookRequest(now, testWebhookBody, 'event-fetch');
  const headers = new Headers(unsigned.headers as Record<string, string>);
  const request = new Request('https://host.example/webhooks/iapstack', {
    method: 'POST',
    headers,
    body: testWebhookBody,
  });
  const event = await verifier.verifyRequest(request);
  assert.equal(event.id, 'event-fetch');
  verifier.close();
});

test('webhook verifier wraps store failures as unavailable', async () => {
  const now = new Date('2026-08-31T10:00:00.000Z');
  const cause = new Error('disk full');
  const verifier = newTestVerifier(now, defaultWebhookBodyLimit, new FailingStore(cause));
  await assert.rejects(
    () => verifier.verifyRequest(signedWebhookRequest(now, testWebhookBody, 'event-1')),
    (error: unknown) => {
      assert.equal(webhookCode(error, 'receiver_unavailable'), true);
      assert(error instanceof WebhookError);
      assert.equal(error.cause, cause);
      return true;
    },
  );
  verifier.close();

  const conflictVerifier = newTestVerifier(
    now,
    defaultWebhookBodyLimit,
    new FailingStore(new WebhookError('event_identity_conflict')),
  );
  await assert.rejects(
    () => conflictVerifier.verifyRequest(signedWebhookRequest(now, testWebhookBody, 'event-2')),
    (error) => webhookCode(error, 'event_identity_conflict'),
  );
  conflictVerifier.close();
});

test('webhook handler owns IAPStack retry statuses', async () => {
  const now = new Date('2026-08-31T10:00:00.000Z');
  const verifier = newTestVerifier(now);
  const handle = verifier.handler(async () => undefined);

  const accepted = await handle(signedWebhookRequest(now, testWebhookBody, 'event-1'));
  assert.equal(accepted.status, 204);

  const duplicate = await handle(signedWebhookRequest(now, testWebhookBody, 'event-1'));
  assert.equal(duplicate.status, 204);

  const badSignature = signedWebhookRequest(now, testWebhookBody, 'event-2');
  badSignature.headers = {
    ...badSignature.headers,
    'IAPStack-Signature': `v2=${'00'.repeat(32)}`,
  };
  const unauthorized = await handle(badSignature);
  assert.equal(unauthorized.status, 401);
  assert.deepEqual(await unauthorized.json(), { code: 'invalid_signature' });

  const conflictVerifier = newTestVerifier(now);
  await conflictVerifier.verifyRequest(signedWebhookRequest(now, testWebhookBody, 'event-3'));
  const altered = testWebhookBody.replace('"version":1', '"version":2');
  const conflict = await conflictVerifier.handler(async () => undefined)(
    signedWebhookRequest(now, altered, 'event-3'),
  );
  assert.equal(conflict.status, 409);
  assert.deepEqual(await conflict.json(), { code: 'event_identity_conflict' });
  conflictVerifier.close();

  const unavailableVerifier = newTestVerifier(
    now,
    defaultWebhookBodyLimit,
    new FailingStore(new Error('disk full')),
  );
  const unavailable = await unavailableVerifier.handler(async () => undefined)(
    signedWebhookRequest(now, testWebhookBody, 'event-4'),
  );
  assert.equal(unavailable.status, 503);
  assert.deepEqual(await unavailable.json(), { code: 'receiver_unavailable' });
  unavailableVerifier.close();

  const callbackVerifier = newTestVerifier(now);
  const callbackFailed = await callbackVerifier.handler(async () => {
    throw new Error('host side effect failed');
  })(signedWebhookRequest(now, testWebhookBody, 'event-5'));
  assert.equal(callbackFailed.status, 503);
  assert.deepEqual(await callbackFailed.json(), { code: 'receiver_unavailable' });
  callbackVerifier.close();

  const method = await handle({
    method: 'GET',
    headers: { 'Content-Type': 'application/json' },
    body: testWebhookBody,
  });
  assert.equal(method.status, 405);
  assert.equal(method.headers.get('Allow'), 'POST');

  const noop = await verifier.handler()(signedWebhookRequest(now, testWebhookBody, 'event-6'));
  assert.equal(noop.status, 204);

  verifier.close();
});
