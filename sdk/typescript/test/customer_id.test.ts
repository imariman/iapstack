import { test } from 'bun:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { Client, RetryPolicy } from '../src/index';

const here = dirname(fileURLToPath(import.meta.url));
const vectorsPath = join(here, '..', '..', '..', 'contracts', 'test-vectors', 'external-customer-ids.json');
const rejectionMessage =
  'external customer ID must be non-empty valid Unicode without surrounding whitespace or control characters';

type CustomerIdVector = { name: string; value: string; accepted: boolean };

/** Loads the shared cases; number segments are UTF-16 code units, so lone surrogates survive. */
function customerIdVectors(): CustomerIdVector[] {
  const document = JSON.parse(readFileSync(vectorsPath, 'utf8')) as {
    cases: { name: string; value: (string | number)[]; accepted: boolean }[];
  };
  assert.ok(document.cases.length > 0);
  return document.cases.map((vector) => ({
    name: vector.name,
    accepted: vector.accepted,
    value: vector.value
      .map((segment) => (typeof segment === 'number' ? String.fromCharCode(segment) : segment))
      .join(''),
  }));
}

function recordingClient(): { client: Client; urls: string[] } {
  const urls: string[] = [];
  const client = new Client({
    baseUrl: 'https://iap.example',
    applicationId: 'application-1',
    applicationToken: 'application-token',
    retryPolicy: new RetryPolicy({ maxAttempts: 1, baseDelayMs: 0, maxDelayMs: 0 }),
    fetch: async (input, init) => {
      const request = new Request(input, init);
      urls.push(request.url);
      const body = request.method === 'POST'
        ? { token: 'iaps_customer', expires_at: '2026-08-24T20:15:00Z' }
        : { customer_id: 'customer-internal', entitlements: [] };
      return new Response(JSON.stringify(body), {
        status: request.method === 'POST' ? 201 : 200,
        headers: { 'content-type': 'application/json' },
      });
    },
  });
  return { client, urls };
}

for (const vector of customerIdVectors()) {
  test(`shared customer ID vector ${vector.accepted ? 'accepted' : 'rejected'} before any request: ${vector.name}`, async () => {
    const { client, urls } = recordingClient();
    const session = { token: 'iaps_customer', expiresAt: new Date('2026-08-24T20:15:00Z'), externalCustomerId: vector.value };
    if (vector.accepted) {
      await client.createCustomerSession(vector.value);
      await client.getEntitlements(session);
      assert.equal(urls.length, 2);
      return;
    }
    const isRejection = (error: unknown) =>
      error instanceof TypeError && error.message === rejectionMessage;
    await assert.rejects(client.createCustomerSession(vector.value), isRejection);
    await assert.rejects(client.getEntitlements(session), isRejection);
    assert.equal(urls.length, 0);
  });
}
