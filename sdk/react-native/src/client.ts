import {
  decodeEntitlementSnapshot,
  decodeRestoreResult,
  decodeVerificationResult,
  EntitlementSnapshot,
  parsePurchaseSubmission,
  PurchaseSubmission,
  RestoreResult,
  VerificationResult,
} from './models';
import { IapStackConfig } from './config';
import {
  IapStackApiError,
  IapStackProtocolError,
  IapStackTimeoutError,
  IapStackTransportError,
} from './errors';

const SDK_VERSION = '0.1.0-dev.1';

const RETRYABLE_STATUS = (statusCode: number): boolean =>
  statusCode === 429 || statusCode >= 500;

/** Runtime client for the IAPStack v1 API from React Native. */
export class IapStackClient {
  constructor(private readonly config: IapStackConfig) {}

  async verifyPurchase(
    submission: PurchaseSubmission,
    requestId?: string,
  ): Promise<VerificationResult> {
    this.validateSubmission(submission);
    const json = await this.request(
      'POST',
      ['v1', 'applications', this.config.applicationId, 'purchases:verify'],
      { body: parsePurchaseSubmission(submission), requestId },
    );
    return this.decode(() => decodeVerificationResult(json));
  }

  async restorePurchases(
    purchases: PurchaseSubmission[],
    requestId?: string,
  ): Promise<RestoreResult> {
    if (!Array.isArray(purchases) || purchases.length < 1 || purchases.length > 100) {
      throw new Error('purchases must contain between 1 and 100 items');
    }
    purchases.forEach((purchase) => this.validateSubmission(purchase));
    const json = await this.request(
      'POST',
      ['v1', 'applications', this.config.applicationId, 'purchases:restore'],
      {
        requestId,
        body: { purchases: purchases.map((item) => parsePurchaseSubmission(item)) },
      },
    );
    return this.decode(() => decodeRestoreResult(json));
  }

  async getEntitlements(
    externalCustomerId: string,
    requestId?: string,
  ): Promise<EntitlementSnapshot> {
    if (!externalCustomerId || !externalCustomerId.trim()) {
      throw new TypeError('externalCustomerId must be a non-empty string');
    }
    const json = await this.request(
      'GET',
      [
        'v1',
        'applications',
        this.config.applicationId,
        'customers',
        externalCustomerId,
        'entitlements',
      ],
      { requestId },
    );
    return this.decode(() => decodeEntitlementSnapshot(json));
  }

  private async request(
    method: 'GET' | 'POST',
    pathSegments: string[],
    params: { requestId?: string; body?: Record<string, unknown> } = {},
  ): Promise<Record<string, unknown>> {
    const uri = this.buildUri(pathSegments);
    for (let attempt = 1; attempt <= this.config.retryPolicy.maxAttempts; attempt++) {
      try {
        return await this.attempt(method, uri, params);
      } catch (error) {
        if (
          error instanceof IapStackApiError &&
          error.retryable &&
          attempt < this.config.retryPolicy.maxAttempts
        ) {
          await delay(this.config.retryPolicy.delayAfter(attempt, Math.random()));
          continue;
        }
        if (
          (error instanceof IapStackTimeoutError ||
            error instanceof IapStackTransportError) &&
          attempt < this.config.retryPolicy.maxAttempts
        ) {
          await delay(this.config.retryPolicy.delayAfter(attempt, Math.random()));
          continue;
        }
        if (error instanceof Error) {
          throw error;
        }
        throw new IapStackTransportError(
          'IAPStack request failed before a response was received',
          error,
        );
      }
    }

    throw new IapStackTransportError('IAPStack request exhausted retry policy');
  }

  private async attempt(
    method: 'GET' | 'POST',
    uri: URL,
    params: { requestId?: string; body?: Record<string, unknown> },
  ): Promise<Record<string, unknown>> {
    const headers = new Headers({
      Accept: 'application/json',
      Authorization: `Bearer ${this.config.customerToken}`,
      'X-IAPStack-SDK': `react-native/${SDK_VERSION}`,
    });
    if (params.requestId && params.requestId.trim()) {
      headers.set('X-Request-ID', params.requestId.trim());
    }

    if (method === 'POST' && params.body) {
      headers.set('Content-Type', 'application/json');
    }

    const abortController = new AbortController();
    const timer = setTimeout(() => {
      abortController.abort();
    }, this.config.timeoutMs);

    try {
      const response = await fetch(uri.toString(), {
        method,
        headers,
        body:
          params.body && method === 'POST'
            ? JSON.stringify(params.body)
            : undefined,
        signal: abortController.signal,
      });

      const text = await readLimitedBody(response, this.config.maxResponseBytes);
      if (response.status >= 200 && response.status < 300) {
        return parseJsonObject(text);
      }
      throw this.apiException(
        response.status,
        text,
        response.headers.get('x-request-id') ?? undefined,
      );
    } catch (error) {
      if (error instanceof IapStackProtocolError || error instanceof IapStackApiError) {
        throw error;
      }
      const domMessage =
        error && typeof error === 'object' && 'name' in error
          ? `${(error as { name?: unknown }).name}`
          : '';
      if (domMessage === 'AbortError' || domMessage === 'TimeoutError') {
        throw new IapStackTimeoutError('IAPStack request timed out', error);
      }
      if (error instanceof Error) {
        throw new IapStackTransportError(
          'IAPStack request failed before a response was available',
          error,
        );
      }
      throw error;
    } finally {
      clearTimeout(timer);
    }
  }

  private decode<T>(decode: () => T): T {
    try {
      return decode();
    } catch (error) {
      if (error instanceof IapStackProtocolError) {
        throw error;
      }
      throw new IapStackProtocolError(
        'IAPStack response did not match the v1 contract',
        error,
      );
    }
  }

  private apiException(
    status: number,
    body: string,
    requestIdHeader?: string,
  ): IapStackApiError {
    let code = 'http_error';
    let message = 'IAPStack returned an unsuccessful response';
    let requestId: string | undefined = requestIdHeader;

    try {
      const parsed = parseJsonObject(body);
      const envelope = parsed.error;
      if (typeof envelope === 'object' && envelope) {
        const candidateCode = (envelope as Record<string, unknown>)['code'];
        const candidateMessage = (envelope as Record<string, unknown>)['message'];
        const candidateRequestId = (envelope as Record<string, unknown>)['request_id'];
        if (typeof candidateCode === 'string' && candidateCode) {
          code = candidateCode;
        }
        if (typeof candidateMessage === 'string' && candidateMessage) {
          message = candidateMessage;
        }
        if (typeof candidateRequestId === 'string' && candidateRequestId) {
          requestId = candidateRequestId;
        }
      }
    } catch {
      // Keep safe generic values.
    }

    return new IapStackApiError(status, code, message, RETRYABLE_STATUS(status), requestId);
  }

  private validateSubmission(submission: PurchaseSubmission): void {
    if (
      !submission ||
      !submission.externalCustomerId ||
      !submission.externalCustomerId.trim()
    ) {
      throw new Error('submission.externalCustomerId is required');
    }
    if (!Array.isArray(submission.claimedProducts) || submission.claimedProducts.length === 0) {
      throw new Error('submission.claimedProducts must contain at least one item');
    }
    if (new Set(submission.claimedProducts).size !== submission.claimedProducts.length) {
      throw new Error('submission.claimedProducts must not contain duplicates');
    }
    if (
      submission.claimedProducts.some(
        (product) => typeof product !== 'string' || !product.trim(),
      )
    ) {
      throw new TypeError('every claimed product id must be a non-empty string');
    }
    if (!isPlainObject(submission.evidence) || Object.keys(submission.evidence).length === 0) {
      throw new Error('submission.evidence is required');
    }
    const maybeBindings = submission.customerBindings ?? [];
    for (const binding of maybeBindings) {
      if (!binding || !binding.kind || !binding.value) {
        throw new Error('customerBindings entries must include kind and value');
      }
      if (
        typeof binding.kind !== 'string' ||
        !binding.kind.trim() ||
        typeof binding.value !== 'string' ||
        !binding.value.trim()
      ) {
        throw new TypeError('customerBindings entries must include non-empty kind/value');
      }
    }
  }

  private buildUri(pathSegments: string[]): URL {
    const base = new URL(this.config.baseUri);
    const basePath = base.pathname === '/' ? '' : base.pathname.replace(/\/+$/, '');
    const appendedPath = pathSegments
      .map((segment) => encodeURIComponent(segment).replace(/%3A/gi, ':'))
      .join('/');
    base.pathname = `${basePath || ''}/${appendedPath}`;
    return base;
  }
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === 'object' && !Array.isArray(value);
}

async function readLimitedBody(response: Response, maxBytes: number): Promise<string> {
  const declaredLength = Number(response.headers.get('content-length'));
  if (Number.isFinite(declaredLength) && declaredLength > maxBytes) {
    await cancelBody(response);
    throw new IapStackProtocolError('IAPStack response exceeded maxResponseBytes');
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
        throw new IapStackProtocolError('IAPStack response exceeded maxResponseBytes');
      }
      chunks.push(value);
    }
    return new TextDecoder().decode(concatBytes(chunks, length));
  }

  const text = await response.text();
  if (byteLength(text) > maxBytes) {
    throw new IapStackProtocolError('IAPStack response exceeded maxResponseBytes');
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

function byteLength(value: string): number {
  if (typeof TextEncoder === 'undefined') {
    return value.length;
  }
  return new TextEncoder().encode(value).byteLength;
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => {
    setTimeout(resolve, ms);
  });
}

function parseJsonObject(raw: string): Record<string, unknown> {
  let payload: unknown;
  try {
    payload = JSON.parse(raw);
  } catch (error) {
    throw new IapStackProtocolError('IAPStack returned invalid JSON', error);
  }
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) {
    throw new IapStackProtocolError('IAPStack response must be a JSON object');
  }
  return payload as Record<string, unknown>;
}
