import { defaultRetryPolicy, RetryPolicy } from './retry';

const DEFAULT_TIMEOUT_MS = 10_000;
const DEFAULT_MAX_RESPONSE_BYTES = 1 << 20;

/** Runtime-only trusted-host configuration for one application. */
export interface Config {
  /** IAPStack origin, optionally including a reverse-proxy path prefix. */
  baseUrl: string;
  /** Application scope encoded in every public API path. */
  applicationId: string;
  /** Durable host bearer retained only in memory by this SDK. */
  applicationToken: string;
  /** Maximum duration of one HTTP attempt, including response streaming. */
  timeoutMs?: number;
  /** Bounded retry policy for idempotent IAPStack operations. */
  retryPolicy?: RetryPolicy;
  /** Maximum accepted JSON response size. */
  maxResponseBytes?: number;
  /** Allows plain HTTP for explicit local development environments. */
  allowInsecureHttp?: boolean;
  /** Optional injected fetch implementation. */
  fetch?: typeof fetch;
}

export interface ResolvedConfig {
  baseUrl: URL;
  applicationId: string;
  applicationToken: string;
  timeoutMs: number;
  retryPolicy: RetryPolicy;
  maxResponseBytes: number;
  fetch: typeof fetch;
}

/** Validates host configuration and fills operational defaults. */
export function resolveConfig(config: Config): ResolvedConfig {
  const timeoutMs = config.timeoutMs ?? DEFAULT_TIMEOUT_MS;
  const maxResponseBytes = config.maxResponseBytes ?? DEFAULT_MAX_RESPONSE_BYTES;
  const retryPolicy = config.retryPolicy ?? defaultRetryPolicy();
  let parsed: URL;
  try {
    parsed = new URL(config.baseUrl);
  } catch {
    throw new TypeError('base URL must be an origin or path prefix');
  }
  if (
    !parsed.host ||
    parsed.search ||
    parsed.hash ||
    parsed.username ||
    parsed.password
  ) {
    throw new TypeError('base URL must be an origin or path prefix');
  }
  if (parsed.protocol !== 'https:' && !(config.allowInsecureHttp && parsed.protocol === 'http:')) {
    throw new TypeError('base URL must use HTTPS');
  }
  if (!config.applicationId.trim() || config.applicationId.includes('/')) {
    throw new TypeError('application ID must be one non-empty path segment');
  }
  validateBearer('application token', config.applicationToken);
  if (timeoutMs <= 0) {
    throw new TypeError('timeout must be positive');
  }
  if (maxResponseBytes <= 0) {
    throw new TypeError('max response bytes must be positive');
  }
  retryPolicy.validate();
  return {
    baseUrl: parsed,
    applicationId: config.applicationId,
    applicationToken: config.applicationToken,
    timeoutMs,
    retryPolicy,
    maxResponseBytes,
    fetch: config.fetch ?? globalThis.fetch.bind(globalThis),
  };
}

/** Rejects empty or whitespace-bearing secrets without echoing them. */
export function validateBearer(name: string, value: string): void {
  if (!value || value.trim() !== value || /\s/u.test(value)) {
    throw new TypeError(`${name} must be a non-empty bearer token`);
  }
}
