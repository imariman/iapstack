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
  ) {
    super(message);
  }
}

export class IapStackTransportError extends IapStackError {}

export class IapStackTimeoutError extends IapStackError {}

export class IapStackProtocolError extends IapStackError {}
