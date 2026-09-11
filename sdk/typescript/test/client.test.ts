import { test } from 'bun:test';
import assert from 'node:assert/strict';
import {
  APIError,
  Client,
  Entitlement,
  ProtocolError,
  RetryPolicy,
  TimeoutError,
  TransportError,
  type Config,
  type CustomerSession,
} from '../src/index';

const testApplicationToken = 'application-token';
const testCustomerToken = 'customer-token';
const testSecretBearer = 'secret with spaces';

type CapturedRequest = {
  method: string;
  url: string;
  headers: Headers;
  body: string;
};

function customerSession(externalCustomerId: string, token = testCustomerToken): CustomerSession {
  return {
    token,
    expiresAt: new Date('2026-08-24T20:15:00.000Z'),
    externalCustomerId,
  };
}

function entitlementSnapshotJSON() {
  return {
    customer_id: 'customer-internal',
    entitlements: [
      {
        key: 'premium',
        access: 'allowed',
        reason: 'purchase_valid',
        effective_starts_at: '2026-08-24T19:00:00Z',
        version: 1,
      },
    ],
  };
}

function abortError(): Error {
  const error = new Error('The operation was aborted');
  error.name = 'AbortError';
  return error;
}

function jsonResponse(status: number, value: unknown, headers: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { 'content-type': 'application/json', ...headers },
  });
}

function newTestClient(
  handler: (input: Request) => Promise<Response> | Response,
  overrides: Partial<Config> = {},
): { client: Client; calls: CapturedRequest[] } {
  const calls: CapturedRequest[] = [];
  const client = new Client({
    baseUrl: 'http://iap.example/proxy',
    applicationId: 'application-1',
    applicationToken: testApplicationToken,
    timeoutMs: 1000,
    retryPolicy: new RetryPolicy({ maxAttempts: 1, baseDelayMs: 0, maxDelayMs: 0 }),
    allowInsecureHttp: true,
    fetch: async (input, init) => {
      const request = new Request(input, init);
      calls.push({
        method: request.method,
        url: request.url,
        headers: request.headers,
        body: await request.clone().text(),
      });
      if (request.signal.aborted) {
        throw abortError();
      }
      return await Promise.race([
        Promise.resolve(handler(request)),
        new Promise<Response>((_resolve, reject) => {
          request.signal.addEventListener(
            'abort',
            () => {
              reject(abortError());
            },
            { once: true },
          );
        }),
      ]);
    },
    ...overrides,
  });
  client.delay = async () => {};
  return { client, calls };
}

test('createCustomerSession sends the application bearer', async () => {
  const { client, calls } = newTestClient(async () =>
    jsonResponse(201, { token: 'iaps_customer', expires_at: '2026-08-24T20:15:00Z' }),
  );

  const session = await client.createCustomerSession('customer-external', {
    requestId: 'request-client-1',
  });

  assert.equal(session.token, 'iaps_customer');
  assert.equal(session.externalCustomerId, 'customer-external');
  assert.equal(session.expiresAt.toISOString(), '2026-08-24T20:15:00.000Z');
  assert.equal(calls.length, 1);
  assert.equal(calls[0]?.method, 'POST');
  assert.equal(calls[0]?.url, 'http://iap.example/proxy/v1/applications/application-1/customer-sessions');
  assert.equal(calls[0]?.headers.get('Authorization'), `Bearer ${testApplicationToken}`);
  assert.equal(calls[0]?.headers.get('X-Request-ID'), 'request-client-1');
  assert.equal(calls[0]?.headers.get('X-IAPStack-SDK')?.startsWith('typescript-host/'), true);
  assert.deepEqual(JSON.parse(calls[0]?.body ?? '{}'), { external_customer_id: 'customer-external' });
});

test('getEntitlements uses the customer session bearer', async () => {
  const { client, calls } = newTestClient(async () => jsonResponse(200, entitlementSnapshotJSON()));

  const snapshot = await client.getEntitlements(customerSession('customer-external'));

  assert.equal(snapshot.customerId, 'customer-internal');
  assert.equal(snapshot.entitlements.length, 1);
  assert.equal(
    snapshot.entitlements[0]?.grantsAccessAt(new Date('2026-08-24T20:00:00Z')),
    true,
  );
  assert.equal(calls[0]?.method, 'GET');
  assert.equal(
    calls[0]?.url,
    'http://iap.example/proxy/v1/applications/application-1/customers/customer-external/entitlements',
  );
  assert.equal(calls[0]?.headers.get('Authorization'), `Bearer ${testCustomerToken}`);
  assert.equal(calls[0]?.body.includes(testApplicationToken), false);
});

test('getEntitlements escapes customer identifiers as one path segment', async () => {
  const { client, calls } = newTestClient(async () => jsonResponse(200, entitlementSnapshotJSON()));

  await client.getEntitlements(customerSession('customer/with space'));
  assert.equal(calls[0]?.url.includes('customer%2Fwith%20space'), true);
});

test('entitlement fails closed at the exclusive effective end', () => {
  const endsAt = new Date('2026-08-26T12:00:00.000Z');
  const entitlement = new Entitlement({
    key: 'premium',
    access: 'allowed',
    reason: 'canceled_at_period_end',
    version: 7,
    effectiveStartsAt: new Date(endsAt.getTime() - 30 * 24 * 60 * 60 * 1000),
    effectiveEndsAt: endsAt,
  });
  assert.equal(entitlement.grantsAccessAt(new Date(endsAt.getTime() - 1)), true);
  assert.equal(entitlement.grantsAccessAt(endsAt), false);
  assert.equal(entitlement.grantsAccessAt(new Date(endsAt.getTime() + 3600_000)), false);
});

test('client retries transient 503 envelopes', async () => {
  let attempts = 0;
  const { client } = newTestClient(
    async () => {
      attempts += 1;
      if (attempts === 1) {
        return jsonResponse(503, {
          error: {
            code: 'provider_unavailable',
            message: 'provider is unavailable',
            request_id: 'request-server-1',
          },
        });
      }
      return jsonResponse(201, { token: 'iaps_customer', expires_at: '2026-08-24T20:15:00Z' });
    },
    { retryPolicy: new RetryPolicy({ maxAttempts: 2, baseDelayMs: 0, maxDelayMs: 0 }) },
  );

  const session = await client.createCustomerSession('customer-external');
  assert.equal(attempts, 2);
  assert.equal(session.token, 'iaps_customer');
});

test('client waits using the retry policy', async () => {
  let waited = 0;
  let attempts = 0;
  const { client } = newTestClient(
    async () => {
      attempts += 1;
      if (attempts === 1) {
        return jsonResponse(429, { error: { code: 'rate_limited', message: 'slow down' } });
      }
      return jsonResponse(201, { token: 'iaps_customer', expires_at: '2026-08-24T20:15:00Z' });
    },
    { retryPolicy: new RetryPolicy({ maxAttempts: 2, baseDelayMs: 40, maxDelayMs: 40 }) },
  );
  client.random = () => 1;
  client.delay = async (ms) => {
    waited = ms;
  };

  await client.createCustomerSession('customer-external');
  assert.equal(waited, 40);
});

test('client exposes stable API errors without extra payload fields', async () => {
  const { client } = newTestClient(async () =>
    jsonResponse(401, {
      error: { code: 'unauthorized', message: 'authentication required', request_id: 'request-server-2' },
      secret: 'must-not-escape',
    }),
  );

  await assert.rejects(
    () => client.createCustomerSession('customer-external'),
    (error: unknown) => {
      assert(error instanceof APIError);
      assert.equal(error.statusCode, 401);
      assert.equal(error.code, 'unauthorized');
      assert.equal(error.requestId, 'request-server-2');
      assert.equal(error.retryable, false);
      assert.equal(error.message.includes('must-not-escape'), false);
      return true;
    },
  );
});

test('client rejects oversized and invalid JSON as protocol errors', async () => {
  const oversized = newTestClient(
    async () => jsonResponse(201, { token: 'too-large-token', expires_at: '2026-08-24T20:15:00Z' }),
    { maxResponseBytes: 8 },
  ).client;
  await assert.rejects(
    () => oversized.createCustomerSession('customer-external'),
    (error) => error instanceof ProtocolError,
  );

  const invalid = newTestClient(async () => new Response('<html>proxy error</html>', { status: 201 })).client;
  await assert.rejects(
    () => invalid.createCustomerSession('customer-external'),
    (error) => error instanceof ProtocolError,
  );
});

test('client turns an attempt deadline into TimeoutError', async () => {
  const { client } = newTestClient(
    async (_request) => {
      await new Promise((_resolve, reject) => {
        // The client aborts via AbortSignal; never complete.
        setTimeout(() => reject(new Error('should have aborted')), 1000);
      });
      return jsonResponse(201, { token: 'iaps_customer', expires_at: '2026-08-24T20:15:00Z' });
    },
    { timeoutMs: 5 },
  );

  await assert.rejects(
    () => client.createCustomerSession('customer-external'),
    (error) => error instanceof TimeoutError,
  );
});

test('client retries and redacts transport errors', async () => {
  let attempts = 0;
  const { client } = newTestClient(
    async () => {
      attempts += 1;
      throw new TypeError('fetch failed: private TLS diagnostics');
    },
    { retryPolicy: new RetryPolicy({ maxAttempts: 2, baseDelayMs: 0, maxDelayMs: 0 }) },
  );

  await assert.rejects(
    () => client.createCustomerSession('customer-external'),
    (error: unknown) => {
      assert(error instanceof TransportError);
      assert.equal(error.message.includes('private'), false);
      return true;
    },
  );
  assert.equal(attempts, 2);
});

test('config requires HTTPS unless explicitly allowed', () => {
  assert.throws(
    () =>
      new Client({
        baseUrl: 'http://iap.example',
        applicationId: 'application-1',
        applicationToken: testApplicationToken,
      }),
  );
});

test('config redacts invalid bearer values', () => {
  assert.throws(
    () =>
      new Client({
        baseUrl: 'https://iap.example',
        applicationId: 'application-1',
        applicationToken: testSecretBearer,
      }),
    (error: unknown) => {
      assert(error instanceof Error);
      assert.equal(error.message.includes(testSecretBearer), false);
      return true;
    },
  );
});

test('getEntitlements rejects empty identities without leaking secrets', async () => {
  const { client, calls } = newTestClient(async () => {
    throw new Error('HTTP should not run for invalid inputs');
  });
  await assert.rejects(() => client.getEntitlements(customerSession('')));
  await assert.rejects(
    () => client.getEntitlements(customerSession('customer-external', testSecretBearer)),
    (error: unknown) => {
      assert(error instanceof Error);
      assert.equal(error.message.includes(testSecretBearer), false);
      return true;
    },
  );
  assert.equal(calls.length, 0);
});

test('createCustomerSession rejects a whitespace customer ID', async () => {
  const { client, calls } = newTestClient(async () => {
    throw new Error('HTTP should not run for invalid inputs');
  });
  await assert.rejects(() => client.createCustomerSession(' '));
  assert.equal(calls.length, 0);
});

test('new Client applies operational defaults', () => {
  const client = new Client({
    baseUrl: 'https://iap.example',
    applicationId: 'application-1',
    applicationToken: testApplicationToken,
  });
  assert.equal(client.config.timeoutMs, 10_000);
  assert.equal(client.config.maxResponseBytes, 1 << 20);
  assert.equal(client.config.retryPolicy.maxAttempts, 3);
  assert.equal(client.config.retryPolicy.baseDelayMs, 250);
  assert.equal(client.config.retryPolicy.maxDelayMs, 2000);
});

test('denied entitlement does not grant access', () => {
  const entitlement = new Entitlement({
    key: 'premium',
    access: 'denied',
    reason: 'refunded',
    version: 2,
  });
  assert.equal(entitlement.grantsAccess(), false);
});

test('RetryPolicy.validate rejects unsafe bounds', () => {
  assert.throws(() =>
    new RetryPolicy({ maxAttempts: 9, baseDelayMs: 1, maxDelayMs: 1000 }).validate(),
  );
  assert.throws(() =>
    new RetryPolicy({ maxAttempts: 3, baseDelayMs: 1000, maxDelayMs: 1 }).validate(),
  );
});

test('RetryPolicy applies full jitter within the cap', () => {
  const policy = new RetryPolicy({ maxAttempts: 3, baseDelayMs: 100, maxDelayMs: 250 });
  assert.equal(policy.delayAfter(1, 0), 0);
  assert.equal(policy.delayAfter(1, 0.5), 50);
  assert.equal(policy.delayAfter(4, 1), 250);
  assert.throws(() => policy.delayAfter(1, 1.1));
});

test('error messages stay free of wrapped secret causes', () => {
  const apiError = new APIError({
    statusCode: 401,
    code: 'unauthorized',
    message: 'authentication required',
    retryable: false,
  });
  assert.equal(apiError.message.includes('unauthorized'), true);
  const timeout = new TimeoutError('IAPStack request timed out', {
    cause: new Error('private TLS diagnostics'),
  });
  assert.equal(timeout.message.includes('private TLS diagnostics'), false);
  const protocol = new ProtocolError('IAPStack returned an invalid JSON object', {
    cause: new Error('secret'),
  });
  assert.equal(protocol.message.includes('secret'), false);
});
