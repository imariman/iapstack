import { test } from 'bun:test';
import assert from 'node:assert/strict';
import { Client, RetryPolicy } from '../src/index';

test('getEntitlements keeps the manual_grant reason and grants access until the end', async () => {
  const client = new Client({
    baseUrl: 'http://iap.example',
    applicationId: 'application-1',
    applicationToken: 'iap_application_token_for_tests',
    timeoutMs: 1000,
    retryPolicy: new RetryPolicy({ maxAttempts: 1, baseDelayMs: 0, maxDelayMs: 0 }),
    allowInsecureHttp: true,
    fetch: async () =>
      new Response(
        JSON.stringify({
          customer_id: 'customer-internal',
          entitlements: [
            {
              key: 'premium',
              access: 'allowed',
              reason: 'manual_grant',
              effective_starts_at: '2026-11-18T10:00:00Z',
              effective_ends_at: '2026-12-18T10:00:00Z',
              version: 2,
            },
          ],
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
  });

  const snapshot = await client.getEntitlements({
    token: 'iaps_customer_token_for_tests',
    expiresAt: new Date('2027-01-01T00:00:00Z'),
    externalCustomerId: 'customer-external',
  });

  const entitlement = snapshot.entitlements[0];
  assert.equal(entitlement?.reason, 'manual_grant');
  assert.equal(entitlement?.grantsAccessAt(new Date('2026-12-01T00:00:00Z')), true);
  assert.equal(entitlement?.grantsAccessAt(new Date('2026-12-18T10:00:00Z')), false);
});
