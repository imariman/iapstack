import { resolveConfig, type Config, type ResolvedConfig, validateBearer } from './config';
import {
  APIError,
  ProtocolError,
  TimeoutError,
  TransportError,
} from './errors';
import {
  Entitlement,
  objectList,
  optionalDate,
  requiredDate,
  requiredInt,
  requiredString,
  type CustomerSession,
  type EntitlementSnapshot,
} from './models';

const SDK_VERSION = '0.1.0-dev.1';
const JSON_CONTENT_TYPE = 'application/json';

export interface RequestOptions {
  requestId?: string;
  signal?: AbortSignal;
}

type DelayFn = (ms: number, signal?: AbortSignal) => Promise<void>;

/** Application-bearer HTTP client for trusted host backends. */
export class Client {
  readonly config: ResolvedConfig;
  delay: DelayFn;
  random: () => number;

  constructor(config: Config) {
    this.config = resolveConfig(config);
    this.delay = waitForRetry;
    this.random = Math.random;
  }

  /**
   * Mints one short-lived customer bearer after the host authenticates its user.
   */
  async createCustomerSession(
    externalCustomerId: string,
    options: RequestOptions = {},
  ): Promise<CustomerSession> {
    validateExternalCustomerId(externalCustomerId);
    const body = await this.request(
      'POST',
      this.config.applicationToken,
      ['v1', 'applications', this.config.applicationId, 'customer-sessions'],
      { external_customer_id: externalCustomerId },
      options,
    );
    return this.decode(() => {
      const token = requiredString(body, 'token');
      const expiresAt = requiredDate(body, 'expires_at');
      return { token, expiresAt, externalCustomerId };
    });
  }

  /**
   * Loads the current projection using a customer session bearer.
   *
   * The public v1 contract authenticates GET
   * /v1/applications/{application_id}/customers/{external_customer_id}/entitlements
   * with customerSessionBearer, not the durable application bearer. Mint a session
   * with createCustomerSession, then either return that token to a mobile client
   * or use it here. Hosts that only need push updates should verify signed
   * webhooks instead of polling.
   */
  async getEntitlements(
    session: CustomerSession,
    options: RequestOptions = {},
  ): Promise<EntitlementSnapshot> {
    validateExternalCustomerId(session.externalCustomerId);
    validateBearer('customer token', session.token);
    const body = await this.request(
      'GET',
      session.token,
      [
        'v1',
        'applications',
        this.config.applicationId,
        'customers',
        session.externalCustomerId,
        'entitlements',
      ],
      undefined,
      options,
    );
    return this.decode(() => {
      const customerId = requiredString(body, 'customer_id');
      const entitlements = objectList(body, 'entitlements').map((item) => {
        const entitlement = new Entitlement({
          key: requiredString(item, 'key'),
          access: requiredString(item, 'access'),
          reason: requiredString(item, 'reason'),
          version: requiredInt(item, 'version'),
          effectiveStartsAt: optionalDate(item, 'effective_starts_at'),
          effectiveEndsAt: optionalDate(item, 'effective_ends_at'),
        });
        if (entitlement.version < 1) {
          throw new Error('version must be at least 1');
        }
        return entitlement;
      });
      return { customerId, entitlements };
    });
  }

  private decode<T>(decode: () => T): T {
    try {
      return decode();
    } catch (error) {
      if (error instanceof ProtocolError) {
        throw error;
      }
      throw new ProtocolError('IAPStack response did not match the v1 contract', { cause: error });
    }
  }

  private async request(
    method: 'GET' | 'POST',
    bearer: string,
    path: string[],
    payload: Record<string, unknown> | undefined,
    options: RequestOptions,
  ): Promise<Record<string, unknown>> {
    const requestId = options.requestId?.trim() ?? '';
    const encoded = payload === undefined ? undefined : JSON.stringify(payload);
    const target = resolveUrl(this.config.baseUrl, path);
    let last: unknown;
    for (let attempt = 1; attempt <= this.config.retryPolicy.maxAttempts; attempt += 1) {
      try {
        const response = await this.attempt(
          method,
          bearer,
          target,
          encoded,
          requestId,
          options.signal,
        );
        if (response.status >= 200 && response.status < 300) {
          return parseJsonObject(response.body);
        }
        const apiError = apiErrorFromResponse(response);
        last = apiError;
        if (!this.shouldRetry(apiError, attempt, options.signal)) {
          throw apiError;
        }
      } catch (error) {
        last = error;
        if (!this.shouldRetry(error, attempt, options.signal)) {
          throw decodeCaught(error);
        }
      }
      try {
        await this.wait(attempt, options.signal);
      } catch {
        throw decodeCaught(last);
      }
    }
    throw decodeCaught(last) ?? new TransportError('IAPStack request exhausted its retry policy');
  }

  private async attempt(
    method: string,
    bearer: string,
    target: URL,
    payload: string | undefined,
    requestId: string,
    signal?: AbortSignal,
  ): Promise<{ status: number; headers: Headers; body: string }> {
    const headers = new Headers({
      Accept: JSON_CONTENT_TYPE,
      Authorization: `Bearer ${bearer}`,
      'X-IAPStack-SDK': `typescript-host/${SDK_VERSION}`,
    });
    if (requestId) {
      headers.set('X-Request-ID', requestId);
    }
    if (payload !== undefined) {
      headers.set('Content-Type', JSON_CONTENT_TYPE);
    }

    const abortController = new AbortController();
    const onParentAbort = () => abortController.abort(signal?.reason);
    if (signal?.aborted) {
      abortController.abort(signal.reason);
    } else {
      signal?.addEventListener('abort', onParentAbort, { once: true });
    }
    const timer = setTimeout(() => {
      abortController.abort();
    }, this.config.timeoutMs);

    try {
      const response = await this.config.fetch(target, {
        method,
        headers,
        body: payload,
        signal: abortController.signal,
      });
      const text = await readLimitedBody(response, this.config.maxResponseBytes);
      return { status: response.status, headers: response.headers, body: text };
    } catch (error) {
      if (error instanceof ProtocolError || error instanceof APIError) {
        throw error;
      }
      if (signal?.aborted) {
        throw new TransportError('IAPStack request failed before a response was received', {
          cause: error,
        });
      }
      if (isAbortError(error)) {
        throw new TimeoutError('IAPStack request timed out', { cause: error });
      }
      throw new TransportError('IAPStack request failed before a response was received', {
        cause: error,
      });
    } finally {
      clearTimeout(timer);
      signal?.removeEventListener('abort', onParentAbort);
    }
  }

  private shouldRetry(error: unknown, attempt: number, signal?: AbortSignal): boolean {
    if (attempt >= this.config.retryPolicy.maxAttempts || signal?.aborted) {
      return false;
    }
    if (error instanceof APIError) {
      return error.retryable;
    }
    return error instanceof TimeoutError || error instanceof TransportError;
  }

  private async wait(attempt: number, signal?: AbortSignal): Promise<void> {
    const delay = this.config.retryPolicy.delayAfter(attempt, this.random());
    if (delay <= 0) {
      return;
    }
    await this.delay(delay, signal);
  }
}

function validateExternalCustomerId(value: string): void {
  if (!value || value.trim() !== value) {
    throw new TypeError('external customer ID must not be empty');
  }
}

function resolveUrl(base: URL, segments: string[]): URL {
  const resolved = new URL(base);
  const basePath = resolved.pathname === '/' ? '' : resolved.pathname.replace(/\/+$/u, '');
  const appended = segments.map((segment) => encodeURIComponent(segment)).join('/');
  resolved.pathname = `${basePath}/${appended}`;
  resolved.search = '';
  resolved.hash = '';
  return resolved;
}

function apiErrorFromResponse(response: {
  status: number;
  headers: Headers;
  body: string;
}): APIError {
  let code = 'http_error';
  let message = 'IAPStack returned an unsuccessful response';
  let requestId = response.headers.get('X-Request-ID')?.trim() ?? '';
  try {
    const parsed = parseJsonObject(response.body);
    const envelope = parsed.error;
    if (envelope && typeof envelope === 'object' && !Array.isArray(envelope)) {
      const errorObject = envelope as Record<string, unknown>;
      if (typeof errorObject.code === 'string' && errorObject.code) {
        code = errorObject.code;
      }
      if (typeof errorObject.message === 'string' && errorObject.message) {
        message = errorObject.message;
      }
      if (typeof errorObject.request_id === 'string' && errorObject.request_id) {
        requestId = errorObject.request_id;
      }
    }
  } catch {
    // Keep safe generic values.
  }
  return new APIError({
    statusCode: response.status,
    code,
    message,
    requestId,
    retryable: response.status === 429 || response.status >= 500,
  });
}

function parseJsonObject(raw: string): Record<string, unknown> {
  let payload: unknown;
  try {
    payload = JSON.parse(raw);
  } catch (error) {
    throw new ProtocolError('IAPStack returned an invalid JSON object', { cause: error });
  }
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) {
    throw new ProtocolError('IAPStack returned an invalid JSON object');
  }
  return payload as Record<string, unknown>;
}

function decodeCaught(error: unknown): Error {
  if (error instanceof ProtocolError || error instanceof APIError) {
    return error;
  }
  if (error instanceof TimeoutError || error instanceof TransportError) {
    return error;
  }
  if (error instanceof Error) {
    try {
      return new ProtocolError('IAPStack response did not match the v1 contract', { cause: error });
    } catch {
      return new ProtocolError('IAPStack response did not match the v1 contract');
    }
  }
  return new TransportError('IAPStack request failed before a response was received', {
    cause: error,
  });
}

async function readLimitedBody(response: Response, maxBytes: number): Promise<string> {
  const declaredLength = Number(response.headers.get('content-length'));
  if (Number.isFinite(declaredLength) && declaredLength > maxBytes) {
    await cancelBody(response);
    throw new ProtocolError('IAPStack response exceeded the configured size limit');
  }

  const body = response.body;
  if (body && typeof body.getReader === 'function') {
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
      if (length > maxBytes) {
        try {
          await reader.cancel();
        } catch {
          // Size cap already exceeded; cancel is best-effort.
        }
        throw new ProtocolError('IAPStack response exceeded the configured size limit');
      }
      chunks.push(value);
    }
    return new TextDecoder().decode(concatBytes(chunks, length));
  }

  const text = await response.text();
  if (new TextEncoder().encode(text).byteLength > maxBytes) {
    throw new ProtocolError('IAPStack response exceeded the configured size limit');
  }
  return text;
}

async function cancelBody(response: Response): Promise<void> {
  const body = response.body;
  if (body && typeof body.cancel === 'function') {
    try {
      await body.cancel();
    } catch {
      // Size cap already exceeded; cancel is best-effort.
    }
  }
}

function concatBytes(chunks: Uint8Array[], length: number): Uint8Array {
  const bytes = new Uint8Array(length);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return bytes;
}

function isAbortError(error: unknown): boolean {
  return Boolean(
    error &&
      typeof error === 'object' &&
      'name' in error &&
      ((error as { name?: string }).name === 'AbortError' ||
        (error as { name?: string }).name === 'TimeoutError'),
  );
}

async function waitForRetry(ms: number, signal?: AbortSignal): Promise<void> {
  if (ms <= 0) {
    return;
  }
  await new Promise<void>((resolve, reject) => {
    const timer = setTimeout(() => {
      signal?.removeEventListener('abort', onAbort);
      resolve();
    }, ms);
    const onAbort = () => {
      clearTimeout(timer);
      reject(signal?.reason ?? new Error('aborted'));
    };
    if (signal?.aborted) {
      onAbort();
      return;
    }
    signal?.addEventListener('abort', onAbort, { once: true });
  });
}
