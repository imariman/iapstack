const ACCESS_ALLOWED = 'allowed';
// RFC 3339 date-time as emitted by IAPStack. `new Date` alone also accepts
// forms such as "2026" or "March 1", and rolls impossible fields such as
// "2026-02-31" or "T24:00" into a later instant, so parseRfc3339 checks each field.
const RFC3339_TIMESTAMP =
  /^(\d{4})-(\d{2})-(\d{2})[Tt](\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(?:[Zz]|[+-](\d{2}):(\d{2}))$/u;

/** One short-lived opaque bearer returned exactly once. */
export interface CustomerSession {
  token: string;
  expiresAt: Date;
  /** Host identity bound to this session at mint time. Not part of the JSON contract. */
  externalCustomerId: string;
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
  get grantsAccess(): boolean {
    return this.grantsAccessAt(new Date());
  }

  /**
   * Reports whether the projection permits access at the supplied instant.
   *
   * Only `access` and `effectiveEndsAt` take part. `effectiveStartsAt` is
   * informational: the server only emits an allowed projection once its period
   * has started, and checking it here would let a clock that runs behind the
   * server deny access right after a purchase. Every IAPStack SDK applies this rule.
   */
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
    throw new Error(`${key} must be an RFC 3339 date-time`);
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
    throw new Error(`${key} must be null or an RFC 3339 date-time`);
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
  const value = parseRfc3339(raw);
  if (!value) {
    throw new Error(`${key} must be an RFC 3339 date-time`);
  }
  return value;
}

/** Parses an RFC 3339 date-time, or returns null for other forms and impossible fields. */
function parseRfc3339(raw: string): Date | null {
  const match = RFC3339_TIMESTAMP.exec(raw);
  if (!match) {
    return null;
  }
  const [year, month, day, hour, minute, second, offsetHour, offsetMinute] = match
    .slice(1)
    .map((field) => Number(field ?? '0'));
  if (
    month < 1 ||
    month > 12 ||
    day < 1 ||
    day > daysInMonth(year, month) ||
    hour > 23 ||
    minute > 59 ||
    second > 59 ||
    offsetHour > 23 ||
    offsetMinute > 59
  ) {
    return null;
  }
  const value = new Date(raw);
  return Number.isNaN(value.getTime()) ? null : value;
}

function daysInMonth(year: number, month: number): number {
  if (month === 2) {
    return year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0) ? 29 : 28;
  }
  return month === 4 || month === 6 || month === 9 || month === 11 ? 30 : 31;
}
