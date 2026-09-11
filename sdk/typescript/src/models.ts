const ACCESS_ALLOWED = 'allowed';

/** One short-lived opaque bearer returned exactly once. */
export interface CustomerSession {
  token: string;
  expiresAt: Date;
}

/** One current application-scoped projection. */
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

  /** Reports whether the projection permits access at the current time. */
  grantsAccess(now = new Date()): boolean {
    return this.grantsAccessAt(now);
  }

  /** Reports whether the projection permits access at the supplied instant. */
  grantsAccessAt(instant: Date): boolean {
    if (this.access !== ACCESS_ALLOWED) {
      return false;
    }
    const endsAt = this.effectiveEndsAt;
    return endsAt === null || instant.getTime() < endsAt.getTime();
  }
}

/** Current projection set for one customer. */
export interface EntitlementSnapshot {
  customerId: string;
  entitlements: Entitlement[];
}

/** Authenticated v1 entitlement.changed webhook payload. */
export class EntitlementChange {
  readonly schemaVersion: number;
  readonly projectId: string;
  readonly applicationId: string;
  readonly customerId: string;
  readonly entitlementId: string;
  readonly entitlementKey: string;
  readonly access: string;
  readonly accessReason: string;
  readonly sourceObservationId: string;
  readonly sourceApplicationId: string;
  readonly sourceProductId: string;
  readonly effectiveStartsAt: Date | null;
  readonly effectiveEndsAt: Date | null;
  readonly version: number;

  constructor(init: {
    schemaVersion: number;
    projectId: string;
    applicationId: string;
    customerId: string;
    entitlementId: string;
    entitlementKey: string;
    access: string;
    accessReason: string;
    sourceObservationId: string;
    sourceApplicationId: string;
    sourceProductId: string;
    effectiveStartsAt?: Date | null;
    effectiveEndsAt?: Date | null;
    version: number;
  }) {
    this.schemaVersion = init.schemaVersion;
    this.projectId = init.projectId;
    this.applicationId = init.applicationId;
    this.customerId = init.customerId;
    this.entitlementId = init.entitlementId;
    this.entitlementKey = init.entitlementKey;
    this.access = init.access;
    this.accessReason = init.accessReason;
    this.sourceObservationId = init.sourceObservationId;
    this.sourceApplicationId = init.sourceApplicationId;
    this.sourceProductId = init.sourceProductId;
    this.effectiveStartsAt = init.effectiveStartsAt ?? null;
    this.effectiveEndsAt = init.effectiveEndsAt ?? null;
    this.version = init.version;
  }

  /** Converts one webhook payload into the provider-neutral projection shape. */
  entitlement(): Entitlement {
    return new Entitlement({
      key: this.entitlementKey,
      access: this.access,
      reason: this.accessReason,
      version: this.version,
      effectiveStartsAt: this.effectiveStartsAt,
      effectiveEndsAt: this.effectiveEndsAt,
    });
  }
}

export function requiredString(json: Record<string, unknown>, key: string): string {
  const value = json[key];
  if (typeof value !== 'string' || value.length === 0) {
    throw new Error(`${key} must be a non-empty string`);
  }
  return value;
}

export function requiredInt(json: Record<string, unknown>, key: string): number {
  const value = json[key];
  if (typeof value !== 'number' || !Number.isInteger(value)) {
    throw new Error(`${key} must be an integer`);
  }
  return value;
}

export function requiredDate(json: Record<string, unknown>, key: string): Date {
  const value = parseDate(json[key], key);
  if (!value) {
    throw new Error(`${key} must be an ISO-8601 timestamp`);
  }
  return value;
}

export function optionalDate(json: Record<string, unknown>, key: string): Date | null {
  const raw = json[key];
  if (raw === null || raw === undefined) {
    return null;
  }
  const value = parseDate(raw, key);
  if (!value) {
    throw new Error(`${key} must be null or an ISO-8601 timestamp`);
  }
  return value;
}

export function objectList(json: Record<string, unknown>, key: string): Record<string, unknown>[] {
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

function parseDate(raw: unknown, key: string): Date | null {
  if (typeof raw !== 'string' || raw.length === 0) {
    return null;
  }
  const value = new Date(raw);
  if (Number.isNaN(value.getTime())) {
    throw new Error(`${key} must be an ISO-8601 timestamp`);
  }
  return value;
}
