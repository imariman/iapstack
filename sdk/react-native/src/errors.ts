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
     * Cooldown in milliseconds the server requested through Retry-After, or
     * undefined when the header was absent or malformed. The client waits at
     * least this long, capped at MAX_RETRY_AFTER_MS, before a retry.
     */
    public readonly retryAfterMs?: number,
  ) {
    super(message);
  }
}

export class IapStackTransportError extends IapStackError {}

export class IapStackTimeoutError extends IapStackError {}

export class IapStackProtocolError extends IapStackError {}
