import { test } from 'bun:test';
import assert from 'node:assert/strict';
import { decodeEntitlementSnapshot } from '../src/index';

test('decodeEntitlementSnapshot keeps the manual_grant reason and grants access until the end', () => {
  const snapshot = decodeEntitlementSnapshot({
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
  });

  const entitlement = snapshot.entitlements[0];
  assert.equal(entitlement?.reason, 'manual_grant');
  assert.equal(entitlement?.grantsAccessAt(new Date('2026-12-01T00:00:00Z')), true);
  assert.equal(entitlement?.grantsAccessAt(new Date('2026-12-18T10:00:00Z')), false);
});
