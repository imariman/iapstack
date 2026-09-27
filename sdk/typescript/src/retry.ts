const DEFAULT_MAX_ATTEMPTS = 3;
const DEFAULT_BASE_DELAY_MS = 250;
const DEFAULT_MAX_DELAY_MS = 2000;
const MAX_BACKOFF_EXPONENT = 20;

/** Bounded full-jitter exponential backoff policy. */
export class RetryPolicy {
  readonly maxAttempts: number;
  readonly baseDelayMs: number;
  readonly maxDelayMs: number;

  constructor(init: Partial<RetryPolicy> = {}) {
    this.maxAttempts = init.maxAttempts ?? DEFAULT_MAX_ATTEMPTS;
    this.baseDelayMs = init.baseDelayMs ?? DEFAULT_BASE_DELAY_MS;
    this.maxDelayMs = init.maxDelayMs ?? DEFAULT_MAX_DELAY_MS;
  }

  /** Full-jitter delay in milliseconds after a failed one-based attempt. */
  delayAfter(attempt: number, randomValue: number): number {
    if (randomValue < 0 || randomValue > 1 || Number.isNaN(randomValue)) {
      throw new RangeError('randomValue must be between 0 and 1');
    }
    if (this.baseDelayMs === 0) {
      return 0;
    }
    let exponent = attempt - 1;
    if (exponent < 0) {
      exponent = 0;
    }
    if (exponent > MAX_BACKOFF_EXPONENT) {
      exponent = MAX_BACKOFF_EXPONENT;
    }
    const delay = Math.min(this.baseDelayMs * 2 ** exponent, this.maxDelayMs);
    return delay * randomValue;
  }

  /** Reports whether retry bounds are safe. */
  validate(): void {
    if (!Number.isInteger(this.maxAttempts) || this.maxAttempts < 1 || this.maxAttempts > 5) {
      throw new TypeError('retry max attempts must be between 1 and 5');
    }
    if (this.baseDelayMs < 0 || this.maxDelayMs < 0 || this.baseDelayMs > this.maxDelayMs) {
      throw new TypeError('retry delays must be non-negative and ordered');
    }
  }
}

/** Flutter-aligned host retry defaults. */
export function defaultRetryPolicy(): RetryPolicy {
  return new RetryPolicy();
}
