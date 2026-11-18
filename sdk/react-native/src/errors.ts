import type {RestoreSkipCounts, VerificationResult} from './models';

export class IapStackError extends Error {
  constructor(
    message: string,
    public readonly cause?: unknown,
  ) {
    super(message);
    this.name = new.target.name;
  }
}

export class IapStackApiError extends IapStackError {
  constructor(
    public readonly statusCode: number,
    public readonly code: string,
    message: string,
    public readonly retryable: boolean,
    public readonly requestId?: string,
    /**
     * Cooldown in milliseconds the server requested through Retry-After: 0
     * when its HTTP-date had already elapsed, undefined when the header was
     * absent or malformed. The client waits `max(jitter, min(retryAfterMs,
     * MAX_RETRY_AFTER_MS))` before a retry.
     */
    public readonly retryAfterMs?: number,
  ) {
    super(message);
  }
}

export class IapStackTransportError extends IapStackError {}

export class IapStackTimeoutError extends IapStackError {}

export class IapStackProtocolError extends IapStackError {}

/**
 * Restore stopped after the server had accepted at least one batch: a later
 * batch failed, or (iOS) StoreKit could not finish a transaction from an
 * accepted batch.
 *
 * Thrown by `IapStackStore.restore()` when the native rejection carries
 * committed results (native `committedRestoreResults` /
 * `IAPStackIncompleteRestoreError.committedResults`, sent across the bridge in
 * the rejection's `userInfo`). Those purchases are already entitled, so
 * callers should apply `committedResults` instead of treating the restore as
 * a total failure. On iOS some of their StoreKit transactions may still be
 * unfinished.
 * `code`/`message` keep the underlying native failure's values, which on iOS
 * can be a StoreKit failure rather than an API failure. When the underlying
 * failure was an API error, `statusCode`, `retryable`, `retryAfterMs` and
 * `requestId` carry its values; otherwise they are undefined. `cause` is the
 * raw native rejection. Rejections without committed results are not wrapped
 * and keep their plain code+message shape.
 */
export class IapStackIncompleteRestoreError extends IapStackError {
  /** HTTP status of the underlying API failure, when it was one. */
  public readonly statusCode?: number;
  /** Whether the underlying API failure may be retried, when it was one. */
  public readonly retryable?: boolean;
  /** Request id of the underlying API failure, when the server sent one. */
  public readonly requestId?: string;
  /** Retry-After cooldown in milliseconds of the underlying API failure, when present. */
  public readonly retryAfterMs?: number;

  constructor(
    message: string,
    /** Machine-readable code of the underlying native failure, when present. */
    public readonly code: string | undefined,
    /** Verification rows the server already accepted before the failure. */
    public readonly committedResults: VerificationResult[],
    /** Store rows that were not submitted before the failure. */
    public readonly skipped: RestoreSkipCounts,
    /** True when Huawei restore had already hit its page cap before the failure. */
    public readonly pageLimitReached: boolean,
    cause?: unknown,
    /** API failure fields from the native rejection's `userInfo`. */
    api: {statusCode?: number; retryable?: boolean; requestId?: string; retryAfterMs?: number} = {},
  ) {
    super(message, cause);
    this.statusCode = api.statusCode;
    this.retryable = api.retryable;
    this.requestId = api.requestId;
    this.retryAfterMs = api.retryAfterMs;
  }
}
