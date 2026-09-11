import { createHash, createHmac, timingSafeEqual } from 'node:crypto';
import { WebhookError } from './errors';
import {
  EntitlementChange,
  optionalDate,
  requiredInt,
  requiredString,
} from './models';

const SIGNATURE_VERSION = 'v2';
const SIGNATURE_PREFIX = `${SIGNATURE_VERSION}=`;
const SIGNATURE_MAC_SEPARATOR = '\n';
const SIGNATURE_BYTES = 32;
const MINIMUM_WEBHOOK_SECRET_BYTES = 32;
const MAXIMUM_WEBHOOK_IDENTITY_LENGTH = 128;
const DEFAULT_WEBHOOK_BODY_LIMIT = 1 << 20;
const DEFAULT_WEBHOOK_TIMESTAMP_TOLERANCE_MS = 5 * 60 * 1000;
const JSON_CONTENT_TYPE = 'application/json';
const HEADER_EVENT_ID = 'IAPStack-Event-ID';
const HEADER_TIMESTAMP = 'IAPStack-Timestamp';
const HEADER_SIGNATURE = 'IAPStack-Signature';

/** Atomically deduplicates authenticated webhook event identities. */
export interface EventStore {
  /**
   * Records one authenticated event ID and body fingerprint.
   * Returns true when the same identity was stored before.
   * A conflicting fingerprint for an existing ID throws a WebhookError.
   */
  remember(eventId: string, fingerprint: Uint8Array): Promise<boolean> | boolean;
}

export interface WebhookConfig {
  /** Webhook signing secret; must contain at least 32 bytes. */
  secret: string | Uint8Array;
  /** Maximum accepted raw webhook body size. */
  bodyLimit?: number;
  /** Allowed clock skew around IAPStack-Timestamp, in milliseconds. */
  timestampToleranceMs?: number;
  /** Atomically deduplicates authenticated event IDs. */
  store: EventStore;
  /** Supplies the current time for replay-window checks. */
  clock?: () => Date;
}

export type WebhookHeaders = Headers | Record<string, string | string[] | undefined>;

/** Framework-agnostic authenticated webhook delivery. */
export interface WebhookRequest {
  method?: string;
  headers?: WebhookHeaders | null;
  body?: Uint8Array | ArrayBuffer | string | null;
}

/** One authenticated, optionally duplicate, entitlement change. */
export interface WebhookEvent {
  id: string;
  timestamp: Date;
  duplicate: boolean;
  change: EntitlementChange;
}

/** Process-local EventStore for tests and single-instance hosts. */
export class MemoryEventStore implements EventStore {
  private readonly events = new Map<string, Buffer>();

  remember(eventId: string, fingerprint: Uint8Array): boolean {
    const next = Buffer.from(fingerprint);
    const existing = this.events.get(eventId);
    if (!existing) {
      this.events.set(eventId, next);
      return false;
    }
    if (existing.length === next.length && timingSafeEqual(existing, next)) {
      return true;
    }
    throw new WebhookError('event_identity_conflict');
  }
}

/** Authenticates v2 IAPStack deliveries and deduplicates event IDs. */
export class WebhookVerifier {
  private secret: Buffer;
  private readonly bodyLimit: number;
  private readonly timestampToleranceMs: number;
  private readonly store: EventStore;
  private readonly clock: () => Date;

  constructor(config: WebhookConfig) {
    const secret = toSecretBuffer(config.secret);
    if (secret.length < MINIMUM_WEBHOOK_SECRET_BYTES) {
      throw new TypeError('webhook signing secret must contain at least 32 bytes');
    }
    if (!config.store) {
      throw new TypeError('webhook event store is required');
    }
    const bodyLimit = config.bodyLimit ?? DEFAULT_WEBHOOK_BODY_LIMIT;
    if (bodyLimit <= 0) {
      throw new TypeError('webhook body limit must be positive');
    }
    const timestampToleranceMs =
      config.timestampToleranceMs ?? DEFAULT_WEBHOOK_TIMESTAMP_TOLERANCE_MS;
    if (timestampToleranceMs <= 0) {
      throw new TypeError('webhook timestamp tolerance must be positive');
    }
    this.secret = Buffer.from(secret);
    this.bodyLimit = bodyLimit;
    this.timestampToleranceMs = timestampToleranceMs;
    this.store = config.store;
    this.clock = config.clock ?? (() => new Date());
  }

  /**
   * Authenticates one HTTP delivery, parses the payload, and deduplicates it.
   * Pass the exact raw body; do not re-serialize parsed JSON.
   */
  async verifyRequest(request: WebhookRequest | Request | null | undefined): Promise<WebhookEvent> {
    if (!request) {
      throw new WebhookError('invalid_body');
    }
    const method = requestMethod(request);
    const headers = requestHeaders(request);
    if (method !== 'POST') {
      throw new WebhookError('method_not_allowed');
    }
    if (!isJSONContentType(headerValue(headers, 'Content-Type'))) {
      throw new WebhookError('content_type_required');
    }
    const eventId = headerValue(headers, HEADER_EVENT_ID);
    if (!validWebhookIdentity(eventId)) {
      throw new WebhookError('invalid_event_id');
    }
    const timestampHeader = headerValue(headers, HEADER_TIMESTAMP);
    const timestamp = this.validTimestamp(timestampHeader);
    if (!timestamp) {
      throw new WebhookError('invalid_timestamp');
    }
    const body = await readRequestBody(request, this.bodyLimit);
    if (
      !this.validSignature(eventId, timestampHeader, body, headerValue(headers, HEADER_SIGNATURE))
    ) {
      throw new WebhookError('invalid_signature');
    }
    const change = decodeEntitlementChange(body);
    const fingerprint = createHash('sha256').update(body).digest();
    const duplicate = await this.store.remember(eventId, fingerprint);
    return {
      id: eventId,
      timestamp,
      duplicate,
      change,
    };
  }

  /** Overwrites the in-memory signing secret. */
  close(): void {
    this.secret.fill(0);
  }

  private validTimestamp(value: string): Date | null {
    if (!value || value.trim() !== value) {
      return null;
    }
    if (!/^-?\d+$/u.test(value)) {
      return null;
    }
    let seconds: bigint;
    try {
      seconds = BigInt(value);
    } catch {
      return null;
    }
    if (seconds.toString() !== value) {
      return null;
    }
    if (seconds > BigInt(Number.MAX_SAFE_INTEGER) || seconds < BigInt(Number.MIN_SAFE_INTEGER)) {
      return null;
    }
    const timestamp = new Date(Number(seconds) * 1000);
    const delta = this.clock().getTime() - timestamp.getTime();
    if (delta < -this.timestampToleranceMs || delta > this.timestampToleranceMs) {
      return null;
    }
    return timestamp;
  }

  private validSignature(eventId: string, timestamp: string, body: Buffer, value: string): boolean {
    if (value.startsWith('v1=')) {
      return false;
    }
    if (!value.startsWith(SIGNATURE_PREFIX) || value.length !== SIGNATURE_PREFIX.length + SIGNATURE_BYTES * 2) {
      return false;
    }
    const providedHex = value.slice(SIGNATURE_PREFIX.length);
    if (!/^[0-9a-fA-F]+$/u.test(providedHex)) {
      return false;
    }
    const provided = Buffer.from(providedHex, 'hex');
    if (provided.length !== SIGNATURE_BYTES) {
      return false;
    }
    const mac = createHmac('sha256', this.secret);
    mac.update(SIGNATURE_VERSION);
    mac.update(SIGNATURE_MAC_SEPARATOR);
    mac.update(eventId);
    mac.update(SIGNATURE_MAC_SEPARATOR);
    mac.update(timestamp);
    mac.update(SIGNATURE_MAC_SEPARATOR);
    mac.update(body);
    const expected = mac.digest();
    return expected.length === provided.length && timingSafeEqual(expected, provided);
  }
}

function requestMethod(request: WebhookRequest | Request): string {
  return isFetchRequest(request) ? request.method : (request.method ?? '');
}

function requestHeaders(request: WebhookRequest | Request): WebhookHeaders {
  return isFetchRequest(request) ? request.headers : (request.headers ?? {});
}

async function readRequestBody(request: WebhookRequest | Request, bodyLimit: number): Promise<Buffer> {
  if (isFetchRequest(request)) {
    return readLimitedStream(request, bodyLimit);
  }
  if (request.body === null || request.body === undefined) {
    throw new WebhookError('invalid_body');
  }
  const body = toBodyBuffer(request.body);
  if (body.length > bodyLimit) {
    throw new WebhookError('body_too_large');
  }
  return body;
}

async function readLimitedStream(request: Request, bodyLimit: number): Promise<Buffer> {
  const body = request.body;
  if (!body || typeof body.getReader !== 'function') {
    const buffer = Buffer.from(await request.arrayBuffer());
    if (buffer.length > bodyLimit) {
      throw new WebhookError('body_too_large');
    }
    return buffer;
  }
  const reader = body.getReader();
  const chunks: Uint8Array[] = [];
  let length = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) {
      break;
    }
    if (!value) {
      continue;
    }
    length += value.byteLength;
    if (length > bodyLimit) {
      try {
        await reader.cancel();
      } catch {
        // Size cap already exceeded; cancel is best-effort.
      }
      throw new WebhookError('body_too_large');
    }
    chunks.push(value);
  }
  return Buffer.concat(chunks, length);
}

function isFetchRequest(request: WebhookRequest | Request): request is Request {
  return typeof Request !== 'undefined' && request instanceof Request;
}

function toBodyBuffer(body: Uint8Array | ArrayBuffer | string): Buffer {
  if (typeof body === 'string') {
    return Buffer.from(body);
  }
  if (body instanceof ArrayBuffer) {
    return Buffer.from(body);
  }
  return Buffer.from(body);
}

function toSecretBuffer(secret: string | Uint8Array): Buffer {
  return typeof secret === 'string' ? Buffer.from(secret) : Buffer.from(secret);
}

function headerValue(headers: WebhookHeaders | null | undefined, name: string): string {
  if (!headers) {
    return '';
  }
  if (typeof Headers !== 'undefined' && headers instanceof Headers) {
    return headers.get(name) ?? '';
  }
  const record = headers as Record<string, string | string[] | undefined>;
  const direct = record[name] ?? record[name.toLowerCase()];
  if (Array.isArray(direct)) {
    return direct[0] ?? '';
  }
  if (typeof direct === 'string') {
    return direct;
  }
  const wanted = name.toLowerCase();
  for (const [key, value] of Object.entries(record)) {
    if (key.toLowerCase() === wanted) {
      if (Array.isArray(value)) {
        return value[0] ?? '';
      }
      return value ?? '';
    }
  }
  return '';
}

function isJSONContentType(value: string): boolean {
  const mediaType = parseMediaType(value);
  return mediaType === JSON_CONTENT_TYPE;
}

function parseMediaType(value: string): string {
  const [raw] = value.split(';', 1);
  return (raw ?? '').trim().toLowerCase();
}

function validWebhookIdentity(value: string): boolean {
  if (!value || value.length > MAXIMUM_WEBHOOK_IDENTITY_LENGTH || value.trim() !== value) {
    return false;
  }
  for (let index = 0; index < value.length; index += 1) {
    const code = value.charCodeAt(index);
    if (code < 0x21 || code > 0x7e) {
      return false;
    }
  }
  return true;
}

function decodeEntitlementChange(body: Buffer): EntitlementChange {
  let payload: unknown;
  try {
    payload = JSON.parse(body.toString('utf8'));
  } catch {
    throw new WebhookError('invalid_event');
  }
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) {
    throw new WebhookError('invalid_event');
  }
  const json = payload as Record<string, unknown>;
  try {
    const schemaVersion = requiredInt(json, 'schema_version');
    const version = requiredInt(json, 'version');
    const change = new EntitlementChange({
      schemaVersion,
      projectId: requiredString(json, 'project_id'),
      applicationId: requiredString(json, 'application_id'),
      customerId: requiredString(json, 'customer_id'),
      entitlementId: requiredString(json, 'entitlement_id'),
      entitlementKey: requiredString(json, 'entitlement_key'),
      access: requiredString(json, 'access'),
      accessReason: requiredString(json, 'access_reason'),
      sourceObservationId: requiredString(json, 'source_observation_id'),
      sourceApplicationId: requiredString(json, 'source_application_id'),
      sourceProductId: requiredString(json, 'source_product_id'),
      effectiveStartsAt: optionalDate(json, 'effective_starts_at'),
      effectiveEndsAt: optionalDate(json, 'effective_ends_at'),
      version,
    });
    if (change.schemaVersion <= 0 || change.version < 1) {
      throw new Error('webhook event metadata is invalid');
    }
    return change;
  } catch {
    throw new WebhookError('invalid_event');
  }
}
