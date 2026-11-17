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
 * A later restore batch failed after earlier batches already committed.
 *
 * Thrown by `IapStackStore.restore()` when the native rejection carries
 * committed results (native `committedRestoreResults` /
 * `IAPStackIncompleteRestoreError.committedResults`, sent across the bridge in
 * the rejection's `userInfo`). Those purchases are already entitled — on iOS
 * their StoreKit transactions may already be finished — so callers should
 * apply `committedResults` instead of treating the restore as a total
 * failure. `code`/`message` keep the underlying native failure's values, and
 * `cause` is the raw native rejection. Rejections without committed results
 * are not wrapped and keep their plain code+message shape.
 */
export class IapStackIncompleteRestoreError extends IapStackError {
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
  ) {
    super(message, cause);
  }
}
