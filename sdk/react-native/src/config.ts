import { IapStackRetryPolicy } from './retry_policy';

export class IapStackConfig {
  readonly baseUri: string;
  readonly applicationId: string;
  readonly customerToken: string;
  readonly timeoutMs: number;
  readonly maxResponseBytes: number;
  readonly allowInsecureHttp: boolean;
  readonly retryPolicy: IapStackRetryPolicy;

  constructor(init: {
    baseUri: string;
    applicationId: string;
    customerToken: string;
    timeoutMs?: number;
    maxResponseBytes?: number;
    allowInsecureHttp?: boolean;
    retryPolicy?: IapStackRetryPolicy;
  }) {
    this.baseUri = init.baseUri;
    this.applicationId = init.applicationId;
    this.customerToken = init.customerToken;
    this.timeoutMs = init.timeoutMs ?? 10_000;
    this.maxResponseBytes = init.maxResponseBytes ?? 1_048_576;
    this.allowInsecureHttp = init.allowInsecureHttp ?? false;
    this.retryPolicy = init.retryPolicy ?? new IapStackRetryPolicy();
    this.validate();
  }

  private validate(): void {
    const uri = new URL(this.baseUri);
    if (!uri.host) {
      throw new TypeError('baseUri must include a host');
    }
    if (uri.search || uri.hash || uri.username || uri.password) {
      throw new TypeError('baseUri must be an origin or path prefix only');
    }
    if (uri.protocol !== 'https:' && !(this.allowInsecureHttp && uri.protocol === 'http:')) {
      throw new TypeError('baseUri must use HTTPS');
    }
    if (!this.applicationId || this.applicationId.includes('/')) {
      throw new TypeError('applicationId must be a non-empty path segment');
    }
    if (!this.customerToken || /\s/.test(this.customerToken)) {
      throw new TypeError('customerToken must be a non-empty bearer without spaces');
    }
    if (this.timeoutMs <= 0) {
      throw new TypeError('timeoutMs must be greater than 0');
    }
    if (this.maxResponseBytes <= 0) {
      throw new TypeError('maxResponseBytes must be greater than 0');
    }
    this.retryPolicy.validate();
  }
}
