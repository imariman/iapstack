export type ProductKind = 'subscription' | 'non_consumable';

const ENTITLEMENT_ACCESS_VALUES = ['allowed', 'denied', 'unresolved'] as const;
const ENTITLEMENT_REASON_VALUES = [
  'purchase_valid',
  'grace_period',
  'pending_payment',
  'canceled_at_period_end',
  'expired',
  'billing_issue',
  'paused',
  'refunded',
  'revoked',
  'provider_decision',
  'unresolved',
] as const;

export interface CustomerBinding {
  kind: string;
  value: string;
}

export interface AppleEvidence {
  signed_transaction: string;
  product_kind: ProductKind;
}

export interface GooglePlayEvidence {
  purchase_token: string;
  product_kind: ProductKind;
}

export interface HuaweiEvidence {
  purchase_data: string;
  signature: string;
  product_kind: ProductKind;
}

export type PurchaseEvidence =
  | AppleEvidence
  | GooglePlayEvidence
  | HuaweiEvidence;

export interface PurchaseSubmission {
  externalCustomerId: string;
  claimedProducts: string[];
  evidence: PurchaseEvidence;
  customerBindings?: CustomerBinding[];
}

export interface Entitlement {
  key: string;
  access: (typeof ENTITLEMENT_ACCESS_VALUES)[number];
  reason: (typeof ENTITLEMENT_REASON_VALUES)[number];
  version: number;
  effectiveStartsAt: Date | null;
  effectiveEndsAt: Date | null;
}

export interface VerificationResult {
  verifiedAt: Date;
  customerId: string;
  entitlements: Entitlement[];
}

export interface RestoreResult {
  results: VerificationResult[];
}

export interface EntitlementSnapshot {
  customerId: string;
  entitlements: Entitlement[];
}

export interface CustomerSession {
  token: string;
  expiresAt: Date;
}

export function parsePurchaseSubmission(
  submission: PurchaseSubmission,
): Record<string, unknown> {
  const payload = {
    external_customer_id: submission.externalCustomerId,
    claimed_products: [...submission.claimedProducts],
    evidence: submission.evidence as Record<string, unknown>,
    ...(submission.customerBindings && submission.customerBindings.length > 0
      ? {
          customer_bindings: submission.customerBindings.map((binding) => ({
            kind: binding.kind,
            value: binding.value,
          })),
        }
      : {}),
  };
  if (!payload.claimed_products.length) {
    throw new Error('claimed_products must not be empty');
  }
  return payload;
}

export function decodeEntitlement(json: Record<string, unknown>): Entitlement {
  return {
    key: _requiredString(json, 'key'),
    access: _requiredString(json, 'access') as Entitlement['access'],
    reason: _requiredString(json, 'reason') as Entitlement['reason'],
    version: _requiredInt(json, 'version'),
    effectiveStartsAt: _optionalDate(json, 'effective_starts_at'),
    effectiveEndsAt: _optionalDate(json, 'effective_ends_at'),
  };
}

export function decodeVerificationResult(
  json: Record<string, unknown>,
): VerificationResult {
  return {
    verifiedAt: _requiredDate(json, 'verified_at'),
    customerId: _requiredString(json, 'customer_id'),
    entitlements: _objectList(json, 'entitlements').map(decodeEntitlement),
  };
}

export function decodeRestoreResult(json: Record<string, unknown>): RestoreResult {
  return {
    results: _objectList(json, 'results').map((entry) =>
      decodeVerificationResult(entry),
    ),
  };
}

export function decodeEntitlementSnapshot(
  json: Record<string, unknown>,
): EntitlementSnapshot {
  return {
    customerId: _requiredString(json, 'customer_id'),
    entitlements: _objectList(json, 'entitlements').map(decodeEntitlement),
  };
}

function _requiredString(json: Record<string, unknown>, key: string): string {
  const value = json[key];
  if (typeof value !== 'string' || value.length === 0) {
    throw new Error(`${key} must be a non-empty string`);
  }
  return value;
}

function _requiredInt(json: Record<string, unknown>, key: string): number {
  const value = json[key];
  if (typeof value !== 'number' || !Number.isInteger(value)) {
    throw new Error(`${key} must be an integer`);
  }
  return value;
}

function _requiredDate(json: Record<string, unknown>, key: string): Date {
  const raw = _requiredString(json, key);
  const value = new Date(raw);
  if (Number.isNaN(value.getTime())) {
    throw new Error(`${key} must be an ISO-8601 timestamp`);
  }
  return value;
}

function _optionalDate(json: Record<string, unknown>, key: string): Date | null {
  const raw = json[key];
  if (raw === null || raw === undefined) {
    return null;
  }
  if (typeof raw !== 'string' || raw.length === 0) {
    throw new Error(`${key} must be null or an ISO-8601 timestamp`);
  }
  const value = new Date(raw);
  if (Number.isNaN(value.getTime())) {
    throw new Error(`${key} must be an ISO-8601 timestamp`);
  }
  return value;
}

function _objectList(json: Record<string, unknown>, key: string): Record<string, unknown>[] {
  const list = json[key];
  if (!Array.isArray(list)) {
    throw new Error(`${key} must be an array`);
  }
  return list.map((item, index) => {
    if (!item || typeof item !== 'object') {
      throw new Error(`${key}[${index}] must be an object`);
    }
    return item as Record<string, unknown>;
  });
}
