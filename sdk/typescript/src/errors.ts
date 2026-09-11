/** Stable non-success v1 envelope. */
export class APIError extends Error {
  readonly statusCode: number;
  readonly code: string;
  readonly requestId: string;
  readonly retryable: boolean;

  constructor(init: {
    statusCode: number;
    code: string;
    message: string;
    requestId?: string;
    retryable: boolean;
  }) {
    super(
      init.requestId
        ? `IAPStack API error (status: ${init.statusCode}, code: ${init.code}, request_id: ${init.requestId}, message: ${init.message})`
        : `IAPStack API error (status: ${init.statusCode}, code: ${init.code}, message: ${init.message})`,
    );
    this.name = 'APIError';
    this.statusCode = init.statusCode;
    this.code = init.code;
    this.requestId = init.requestId ?? '';
    this.retryable = init.retryable;
  }
}

/** Network failure before a complete HTTP response. */
export class TransportError extends Error {
  constructor(
    message = 'IAPStack request failed before a response was received',
    options?: { cause?: unknown },
  ) {
    super(message);
    this.name = 'TransportError';
    if (options && 'cause' in options) {
      (this as Error & { cause?: unknown }).cause = options.cause;
    }
  }
}

/** Bounded HTTP attempt that exceeded its deadline. */
export class TimeoutError extends Error {
  constructor(message = 'IAPStack request timed out', options?: { cause?: unknown }) {
    super(message);
    this.name = 'TimeoutError';
    if (options && 'cause' in options) {
      (this as Error & { cause?: unknown }).cause = options.cause;
    }
  }
}

/** Response that did not match the versioned JSON contract. */
export class ProtocolError extends Error {
  constructor(
    message = 'IAPStack response did not match the v1 contract',
    options?: { cause?: unknown },
  ) {
    super(message);
    this.name = 'ProtocolError';
    if (options && 'cause' in options) {
      (this as Error & { cause?: unknown }).cause = options.cause;
    }
  }
}

/** Fail-closed webhook authentication or replay failure. */
export class WebhookError extends Error {
  readonly code: string;

  constructor(code = 'webhook verification failed') {
    super(code || 'webhook verification failed');
    this.name = 'WebhookError';
    this.code = code;
  }
}
