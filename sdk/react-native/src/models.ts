export type ProductKind = 'subscription' | 'non_consumable';

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

export type PurchaseEvidence = Record<string, unknown>;

export interface PurchaseSubmission {
  externalCustomerId: string;
  claimedProducts: string[];
  evidence: PurchaseEvidence;
  customerBindings?: CustomerBinding[];
}

export class Entitlement {
  readonly key: string;
  readonly access: string;
  readonly reason: string;
  readonly version: number;
  readonly effectiveStartsAt: Date | null;
  readonly effectiveEndsAt: Date | null;

  constructor(init: {
    key: string;
    access: string;
    reason: string;
    version: number;
    effectiveStartsAt?: Date | null;
    effectiveEndsAt?: Date | null;
  }) {
    this.key = init.key;
    this.access = init.access;
    this.reason = init.reason;
    this.version = init.version;
    this.effectiveStartsAt = init.effectiveStartsAt ?? null;
    this.effectiveEndsAt = init.effectiveEndsAt ?? null;
  }

  get grantsAccess(): boolean {
    return this.grantsAccessAt(new Date());
  }

  grantsAccessAt(instant: Date): boolean {
    if (this.access !== 'allowed') {
      return false;
    }
    const endsAt = this.effectiveEndsAt;
    return endsAt === null || instant.getTime() < endsAt.getTime();
  }
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

export function parsePurchaseSubmission(
  submission: PurchaseSubmission,
): Record<string, unknown> {
  const payload: Record<string, unknown> = {
    external_customer_id: submission.externalCustomerId,
    claimed_products: [...submission.claimedProducts],
    evidence: { ...submission.evidence },
  };
  if (submission.customerBindings && submission.customerBindings.length > 0) {
    payload.customer_bindings = submission.customerBindings.map((binding) => ({
      kind: binding.kind,
      value: binding.value,
    }));
  }
  if (!(payload.claimed_products as string[]).length) {
    throw new Error('claimed_products must not be empty');
  }
  return payload;
}

export function decodeEntitlement(json: Record<string, unknown>): Entitlement {
  return new Entitlement({
    key: requiredString(json, 'key'),
    access: requiredString(json, 'access'),
    reason: requiredString(json, 'reason'),
    version: requiredInt(json, 'version'),
    effectiveStartsAt: optionalDate(json, 'effective_starts_at'),
    effectiveEndsAt: optionalDate(json, 'effective_ends_at'),
  });
}

export function decodeVerificationResult(
  json: Record<string, unknown>,
): VerificationResult {
  return {
    verifiedAt: requiredDate(json, 'verified_at'),
    customerId: requiredString(json, 'customer_id'),
    entitlements: objectList(json, 'entitlements').map(decodeEntitlement),
  };
}

export function decodeRestoreResult(json: Record<string, unknown>): RestoreResult {
  return {
    results: objectList(json, 'results').map((entry) =>
      decodeVerificationResult(entry),
    ),
  };
}

export function decodeEntitlementSnapshot(
  json: Record<string, unknown>,
): EntitlementSnapshot {
  return {
    customerId: requiredString(json, 'customer_id'),
    entitlements: objectList(json, 'entitlements').map(decodeEntitlement),
  };
}

function requiredString(json: Record<string, unknown>, key: string): string {
  const value = json[key];
  if (typeof value !== 'string' || value.length === 0) {
    throw new Error(`${key} must be a non-empty string`);
  }
  return value;
}

function requiredInt(json: Record<string, unknown>, key: string): number {
  const value = json[key];
  if (typeof value !== 'number' || !Number.isInteger(value)) {
    throw new Error(`${key} must be an integer`);
  }
  return value;
}

function requiredDate(json: Record<string, unknown>, key: string): Date {
  const raw = requiredString(json, key);
  const value = new Date(raw);
  if (Number.isNaN(value.getTime())) {
    throw new Error(`${key} must be an ISO-8601 timestamp`);
  }
  return value;
}

function optionalDate(json: Record<string, unknown>, key: string): Date | null {
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

function objectList(json: Record<string, unknown>, key: string): Record<string, unknown>[] {
  const list = json[key];
  if (!Array.isArray(list)) {
    throw new Error(`${key} must be an array`);
  }
  return list.map((item, index) => {
    if (!item || typeof item !== 'object' || Array.isArray(item)) {
      throw new Error(`${key}[${index}] must be an object`);
    }
    return item as Record<string, unknown>;
  });
}
