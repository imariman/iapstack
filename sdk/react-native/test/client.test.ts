import { test } from 'bun:test';
import assert from 'node:assert/strict';
import {
  appleEvidence,
  googlePlayEvidence,
  huaweiEvidence,
  IapStackApiError,
  IapStackClient,
  IapStackConfig,
  IapStackRetryPolicy,
  IapStackTimeoutError,
} from '../src/index';

type MockRequest = {
  url: string;
  init: RequestInit;
};

function withMockFetch(
  handler: (input: RequestInfo | URL, init: RequestInit) => Promise<Response>,
): { calls: MockRequest[]; restore: () => void } {
  const originalFetch = globalThis.fetch;
  const calls: MockRequest[] = [];
  globalThis.fetch = async (input: RequestInfo | URL, init: RequestInit = {}) => {
    calls.push({
      url: input instanceof URL ? input.toString() : `${input}`,
      init,
    });
    return handler(input, init);
  };

  return {
    calls,
    restore() {
      globalThis.fetch = originalFetch;
    },
  };
}

function verificationPayload() {
  return {
    verified_at: '2026-09-09T00:00:00.000Z',
    customer_id: 'customer-123',
    entitlements: [
      {
        key: 'premium',
        access: 'allowed',
        reason: 'purchase_valid',
        version: 1,
        effective_starts_at: '2026-09-09T00:00:00.000Z',
        effective_ends_at: null,
      },
    ],
  };
}

function restorePayload() {
  return {
    results: [
      {
        verified_at: '2026-09-09T00:00:00.000Z',
        customer_id: 'customer-123',
        entitlements: [],
      },
    ],
  };
}

function entitlementPayload() {
  return {
    customer_id: 'customer-123',
    entitlements: [],
  };
}

function clientConfig(overrides: Partial<ConstructorParameters<typeof IapStackConfig>[0]> = {}) {
  return new IapStackConfig({
    baseUri: 'https://iapstack.test/api',
    applicationId: 'app-1',
    customerToken: 'token',
    ...overrides,
  });
}

const samplePurchase = {
  externalCustomerId: 'customer-123',
  claimedProducts: ['premium_annual'],
  evidence: googlePlayEvidence({
    purchaseToken: 'token-abc',
    productKind: 'subscription',
  }),
};


test('verifyPurchase sends correct method, path, headers, and payload', async () => {
  const { calls, restore } = withMockFetch(async (_input, _init) => {
    return new Response(JSON.stringify(verificationPayload()), {
      status: 200,
      headers: { 'content-type': 'application/json' },
    });
  });

  try {
    const client = new IapStackClient(clientConfig());
    const result = await client.verifyPurchase(samplePurchase, 'request-123');

    assert.equal(result.customerId, 'customer-123');
    assert.equal(calls.length, 1);
    assert.equal(
      calls[0].url,
      'https://iapstack.test/api/v1/applications/app-1/purchases:verify',
    );

    const headers = new Headers(calls[0].init.headers as HeadersInit);
    assert.equal(headers.get('Accept'), 'application/json');
    assert.equal(headers.get('Authorization'), 'Bearer token');
    assert.equal(headers.get('X-IAPStack-SDK'), 'react-native/0.1.0-dev.1');
    assert.equal(headers.get('X-Request-ID'), 'request-123');
    assert.equal(headers.get('Content-Type'), 'application/json');

    const requestBody = JSON.parse((calls[0].init.body as string) || '{}');
    assert.equal(requestBody.external_customer_id, 'customer-123');
    assert.deepEqual(requestBody.claimed_products, ['premium_annual']);
    assert.equal(requestBody.evidence.purchase_token, 'token-abc');
  } finally {
    restore();
  }
});

test('restorePurchases retries only on retryable status codes', async () => {
  let attempt = 0;
  const { calls, restore } = withMockFetch(async (_input, _init) => {
    attempt += 1;
    if (attempt === 1) {
      return new Response(
        JSON.stringify({
          error: {
            code: 'service_unavailable',
            message: 'temporary',
          },
        }),
        {
          status: 503,
          headers: {
            'content-type': 'application/json',
            'x-request-id': 'req-503',
          },
        },
      );
    }
    return new Response(JSON.stringify(restorePayload()), {
      status: 200,
      headers: {
        'content-type': 'application/json',
        'x-request-id': 'req-ok',
      },
    });
  });

  try {
    const client = new IapStackClient(
      clientConfig({
        retryPolicy: new IapStackRetryPolicy({
          maxAttempts: 2,
          baseDelayMs: 0,
          maxDelayMs: 0,
        }),
      }),
    );
    const result = await client.restorePurchases([samplePurchase]);

    assert.equal(attempt, 2);
    assert.equal(calls.length, 2);
    assert.equal(
      calls[0].url,
      'https://iapstack.test/api/v1/applications/app-1/purchases:restore',
    );
    assert.equal(calls[1].url, 'https://iapstack.test/api/v1/applications/app-1/purchases:restore');
    assert.equal(result.results[0].customerId, 'customer-123');
  } finally {
    restore();
  }
});

test('non-retryable API errors are surfaced without another attempt', async () => {
  let attempt = 0;
  const { calls, restore } = withMockFetch(async () => {
    attempt += 1;
    return new Response(
      JSON.stringify({
        error: {
          code: 'invalid_request',
          message: 'invalid',
        },
      }),
      {
        status: 400,
        headers: { 'content-type': 'application/json' },
      },
    );
  });

  try {
    const client = new IapStackClient(
      clientConfig({
        retryPolicy: new IapStackRetryPolicy({
          maxAttempts: 3,
          baseDelayMs: 0,
          maxDelayMs: 0,
        }),
      }),
    );

    await assert.rejects(
      () => client.getEntitlements('customer-123'),
      (error) => {
        assert(error instanceof IapStackApiError);
        assert.equal((error as IapStackApiError).statusCode, 400);
        return true;
      },
    );
    assert.equal(attempt, 1);
    assert.equal(calls.length, 1);
  } finally {
    restore();
  }
});

test('request timeouts are converted into IapStackTimeoutError', async () => {
  const { restore } = withMockFetch(async (_input, init) => {
    return new Promise((_resolve, reject) => {
      init.signal?.addEventListener('abort', () => {
        const timeout = new Error('timed out') as Error & { name: string };
        timeout.name = 'AbortError';
        reject(timeout);
      });
    });
  });

  try {
    const client = new IapStackClient(
      clientConfig({
        timeoutMs: 5,
      }),
    );

    await assert.rejects(
      () => client.verifyPurchase(samplePurchase),
      (error) => error instanceof IapStackTimeoutError,
    );
  } finally {
    restore();
  }
});

test('duplicate claimed products are rejected before making requests', async () => {
  const { calls, restore } = withMockFetch(async () => {
    throw new Error('should not be hit');
  });

  try {
    const client = new IapStackClient(clientConfig());
    await assert.rejects(
      () =>
        client.restorePurchases([
          {
            ...samplePurchase,
            claimedProducts: ['same', 'same'],
          },
        ]),
      (error) => {
        assert.match((error as Error).message, /must not contain duplicates/);
        return true;
      },
    );
    assert.equal(calls.length, 0);
  } finally {
    restore();
  }
});

test('evidence helper builders validate required input', () => {
  const apple = appleEvidence({ signedTransaction: 'txn', productKind: 'subscription' });
  assert.equal(apple.product_kind, 'subscription');
  assert.equal(apple.signed_transaction, 'txn');

  const google = googlePlayEvidence({ purchaseToken: 'play-token', productKind: 'non_consumable' });
  assert.equal(google.product_kind, 'non_consumable');

  const huawei = huaweiEvidence({
    purchaseData: '{"ok":true}',
    signature: 'sig',
    productKind: 'subscription',
  });
  assert.equal(huawei.signature, 'sig');

  assert.throws(() => {
    googlePlayEvidence({} as never);
  });
});
