import { defaultRetryPolicy, MAX_TIMER_MS, RetryPolicy } from './retry.js';

const DEFAULT_TIMEOUT_MS = 10_000;
const DEFAULT_MAX_RESPONSE_BYTES = 1 << 20;

/** Minimal fetch signature the client needs; any WHATWG-compatible fetch works. */
export type FetchFn = (input: URL | RequestInfo, init?: RequestInit) => Promise<Response>;

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
  fetch?: FetchFn;
}

export interface ResolvedConfig {
  baseUrl: URL;
  applicationId: string;
  applicationToken: string;
  timeoutMs: number;
  retryPolicy: RetryPolicy;
  maxResponseBytes: number;
  fetch: FetchFn;
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
  if (
    typeof config.applicationId !== 'string' ||
    !config.applicationId.trim() ||
    config.applicationId.includes('/')
  ) {
    throw new TypeError('application ID must be one non-empty path segment');
  }
  validateBearer('application token', config.applicationToken);
  // timeoutMs feeds setTimeout directly, so it must be a duration the timer honors.
  if (!Number.isInteger(timeoutMs) || timeoutMs < 1 || timeoutMs > MAX_TIMER_MS) {
    throw new TypeError('timeout must be an integer between 1 and 2147483647 ms');
  }
  if (!isPositiveFinite(maxResponseBytes)) {
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

/** Reports whether a numeric option is a real, strictly positive number (not NaN/Infinity). */
export function isPositiveFinite(value: number): boolean {
  return typeof value === 'number' && Number.isFinite(value) && value > 0;
}

/** Rejects empty or whitespace-bearing secrets without echoing them. */
export function validateBearer(name: string, value: string): void {
  if (typeof value !== 'string' || !value || value.trim() !== value || /\s/u.test(value)) {
    throw new TypeError(`${name} must be a non-empty bearer token`);
  }
}
