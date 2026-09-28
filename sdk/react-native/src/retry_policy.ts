/**
 * Upper bound for a server-requested Retry-After cooldown, so one response
 * cannot stall a caller indefinitely. Every IAPStack SDK uses 30 seconds.
 */
export const MAX_RETRY_AFTER_MS = 30_000;

/** Bounded full-jitter exponential backoff policy. */
export class IapStackRetryPolicy {
  readonly maxAttempts: number;
  readonly baseDelayMs: number;
  readonly maxDelayMs: number;

  constructor(init: Partial<IapStackRetryPolicy> = {}) {
    this.maxAttempts = init.maxAttempts ?? 3;
    this.baseDelayMs = init.baseDelayMs ?? 250;
    this.maxDelayMs = init.maxDelayMs ?? 2000;
    this.validate();
  }

  /**
   * Full-jitter delay in milliseconds after a failed one-based attempt.
   *
   * When the failed response carried a Retry-After cooldown, pass it as
   * `retryAfterMs`: the result is then the larger of the jitter delay and the
   * cooldown, capped at MAX_RETRY_AFTER_MS, so a retry never lands inside a
   * cooldown the server asked for.
   */
  delayAfter(attempt: number, randomValue: number, retryAfterMs?: number): number {
    const jitter = this.jitterDelay(attempt, randomValue);
    if (retryAfterMs === undefined || !(retryAfterMs > 0)) {
      return jitter;
    }
    return Math.min(Math.max(jitter, retryAfterMs), MAX_RETRY_AFTER_MS);
  }

  private jitterDelay(attempt: number, randomValue: number): number {
    if (randomValue < 0 || randomValue > 1 || Number.isNaN(randomValue)) {
      throw new RangeError('randomValue must be between 0 and 1');
    }
    if (this.baseDelayMs <= 0) {
      return 0;
    }
    const exponent = attempt - 1;
    const multiplier = Math.min(1 << Math.max(0, Math.min(20, exponent)), 1_000_000);
    const capped = Math.min(this.baseDelayMs * multiplier, this.maxDelayMs);
    return Math.round(capped * randomValue);
  }

  validate(): void {
    if (!Number.isInteger(this.maxAttempts) || this.maxAttempts < 1 || this.maxAttempts > 5) {
      throw new TypeError('maxAttempts must be an integer between 1 and 5');
    }
    if (this.baseDelayMs < 0 || this.maxDelayMs < 0 || this.baseDelayMs > this.maxDelayMs) {
      throw new TypeError('retry delays must be non-negative and ordered');
    }
  }
}
