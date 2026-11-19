import { test } from 'bun:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { googlePlayEvidence, IapStackClient, IapStackConfig } from '../src/index';

const here = dirname(fileURLToPath(import.meta.url));
const vectorsPath = join(here, '..', '..', '..', 'contracts', 'test-vectors', 'external-customer-ids.json');
const rejectionMessage =
  'externalCustomerId must be non-empty valid Unicode without surrounding whitespace or control characters';

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

async function withRecordingFetch(run: (urls: string[]) => Promise<void>): Promise<void> {
  const originalFetch = globalThis.fetch;
  const urls: string[] = [];
  globalThis.fetch = (async (input: RequestInfo | URL) => {
    urls.push(`${input}`);
    const body = `${input}`.endsWith('/entitlements')
      ? { customer_id: 'customer-internal', entitlements: [] }
      : { verified_at: '2026-09-09T00:00:00.000Z', customer_id: 'customer-internal', entitlements: [] };
    return new Response(JSON.stringify(body), { status: 200, headers: { 'content-type': 'application/json' } });
  }) as typeof fetch;
  try {
    await run(urls);
  } finally {
    globalThis.fetch = originalFetch;
  }
}

for (const vector of customerIdVectors()) {
  test(`shared customer ID vector ${vector.accepted ? 'accepted' : 'rejected'} before any request: ${vector.name}`, async () => {
    await withRecordingFetch(async (urls) => {
      const client = new IapStackClient(new IapStackConfig({
        baseUri: 'https://iapstack.test/api', applicationId: 'app-1', customerToken: 'token',
      }));
      const purchase = {
        externalCustomerId: vector.value,
        claimedProducts: ['premium_annual'],
        evidence: googlePlayEvidence({ purchaseToken: 'token-abc', productKind: 'subscription' }),
      };
      if (vector.accepted) {
        await client.getEntitlements(vector.value);
        await client.verifyPurchase(purchase);
        assert.equal(urls.length, 2);
        return;
      }
      const isRejection = (error: unknown) =>
        error instanceof TypeError && error.message === rejectionMessage;
      await assert.rejects(client.getEntitlements(vector.value), isRejection);
      await assert.rejects(client.verifyPurchase(purchase), isRejection);
      assert.equal(urls.length, 0);
    });
  });
}
