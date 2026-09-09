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
import { IapStackRetryPolicy } from './retry_policy';

const SDK_VERSION = '0.1.0-dev.1';
const DEFAULT_RETRY_POLICY = new IapStackRetryPolicy();

const RETRYABLE_STATUS = (statusCode: number): boolean =>
  statusCode === 429 || statusCode >= 500;

/** Runtime client for the IAPStack v1 API from React Native. */
export class IapStackClient {
  private readonly config: IapStackConfig;
  private readonly retryPolicy: IapStackRetryPolicy;

  constructor(config: IapStackConfig) {
    this.config = config;
    this.retryPolicy = config.retryPolicy ?? DEFAULT_RETRY_POLICY;
  }

  async verifyPurchase(
    submission: PurchaseSubmission,
    requestId?: string,
  ): Promise<VerificationResult> {
    this._validateSubmission(submission);
    const json = await this._request(
      'POST',
      ['v1', 'applications', this.config.applicationId, 'purchases:verify'],
      { body: parsePurchaseSubmission(submission), requestId },
    );
    return decodeVerificationResult(json);
  }

  async restorePurchases(
    purchases: PurchaseSubmission[],
    requestId?: string,
  ): Promise<RestoreResult> {
    if (!Array.isArray(purchases) || purchases.length < 1 || purchases.length > 100) {
      throw new Error('purchases must contain between 1 and 100 items');
    }
    purchases.forEach((purchase) => this._validateSubmission(purchase));
    const json = await this._request(
      'POST',
      ['v1', 'applications', this.config.applicationId, 'purchases:restore'],
      {
        requestId,
        body: { purchases: purchases.map((item) => parsePurchaseSubmission(item)) },
      },
    );
    return decodeRestoreResult(json);
  }

  async getEntitlements(
    externalCustomerId: string,
    requestId?: string,
  ): Promise<EntitlementSnapshot> {
    if (!externalCustomerId || !externalCustomerId.trim()) {
      throw new TypeError('externalCustomerId must be a non-empty string');
    }
    const json = await this._request(
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
    return decodeEntitlementSnapshot(json);
  }

  private async _request(
    method: 'GET' | 'POST',
    pathSegments: string[],
    params: { requestId?: string; body?: Record<string, unknown> } = {},
  ): Promise<Record<string, unknown>> {
    const uri = this._buildUri(pathSegments);
    for (let attempt = 1; attempt <= this.retryPolicy.maxAttempts; attempt++) {
      try {
        return await this._attempt(method, uri, params);
      } catch (error) {
        if (
          error instanceof IapStackApiError &&
          error.retryable &&
          attempt < this.retryPolicy.maxAttempts
        ) {
          await delay(this.retryPolicy.delayAfter(attempt, Math.random()));
          continue;
        }
        if (
          (error instanceof IapStackTimeoutError ||
            error instanceof IapStackTransportError) &&
          attempt < this.retryPolicy.maxAttempts
        ) {
          await delay(this.retryPolicy.delayAfter(attempt, Math.random()));
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

  private async _attempt(
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

      const text = await response.text();
      if (toBytes(text) > this.config.maxResponseBytes) {
        throw new IapStackProtocolError('IAPStack response exceeded maxResponseBytes');
      }
      if (response.status >= 200 && response.status < 300) {
        return parseJsonObject(text);
      }
      throw this._apiException(
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

  private _apiException(
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
    } catch (_error) {
      // Keep safe generic values.
    }

    return new IapStackApiError(status, code, message, RETRYABLE_STATUS(status), requestId);
  }

  private _validateSubmission(submission: PurchaseSubmission): void {
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
    if (!submission.evidence || typeof submission.evidence !== 'object') {
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

  private _buildUri(pathSegments: string[]): URL {
    const base = new URL(this.config.baseUri);
    const basePath = base.pathname === '/' ? '' : base.pathname.replace(/\/+$/, '');
    const appendedPath = pathSegments
      .map((segment) => encodeURIComponent(segment).replace(/%3A/gi, ':'))
      .join('/');
    base.pathname = `${basePath || ''}/${appendedPath}`;
    return base;
  }
}

export function createClient(config: IapStackConfig): IapStackClient {
  return new IapStackClient(config);
}

function toBytes(value: string): number {
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
