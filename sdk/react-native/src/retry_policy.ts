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

  delayAfter(attempt: number, randomValue: number): number {
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
