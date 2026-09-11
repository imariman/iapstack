import { test } from 'bun:test';
import assert from 'node:assert/strict';
import {
  appleEvidence,
  Entitlement,
  googlePlayEvidence,
  huaweiEvidence,
  IapStackApiError,
  IapStackClient,
  IapStackConfig,
  IapStackProtocolError,
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
    assert.equal(result.entitlements[0]?.grantsAccess, true);
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

test('empty and non-object evidence is rejected before making requests', async () => {
  const { calls, restore } = withMockFetch(async () => {
    throw new Error('should not be hit');
  });

  try {
    const client = new IapStackClient(clientConfig());
    await assert.rejects(
      () => client.verifyPurchase({ ...samplePurchase, evidence: {} }),
      (error) => {
        assert.match((error as Error).message, /evidence is required/);
        return true;
      },
    );
    await assert.rejects(
      () =>
        client.verifyPurchase({
          ...samplePurchase,
          evidence: [] as unknown as Record<string, unknown>,
        }),
      (error) => {
        assert.match((error as Error).message, /evidence is required/);
        return true;
      },
    );
    assert.equal(calls.length, 0);
  } finally {
    restore();
  }
});

test('rejects oversized responses as protocol errors', async () => {
  const { restore } = withMockFetch(async () => {
    return new Response('{"value":"too-large"}', {
      status: 200,
      headers: { 'content-type': 'application/json' },
    });
  });

  try {
    const client = new IapStackClient(clientConfig({ maxResponseBytes: 8 }));
    await assert.rejects(
      () => client.verifyPurchase(samplePurchase),
      (error) => error instanceof IapStackProtocolError,
    );
  } finally {
    restore();
  }
});

test('aborts when Content-Length exceeds maxResponseBytes', async () => {
  const { restore } = withMockFetch(async () => {
    return new Response('{"ok":true}', {
      status: 200,
      headers: {
        'content-type': 'application/json',
        'content-length': '999999',
      },
    });
  });

  try {
    const client = new IapStackClient(clientConfig({ maxResponseBytes: 32 }));
    await assert.rejects(
      () => client.verifyPurchase(samplePurchase),
      (error) => error instanceof IapStackProtocolError,
    );
  } finally {
    restore();
  }
});

test('wraps contract mismatches as IapStackProtocolError', async () => {
  const { restore } = withMockFetch(async () => {
    return new Response(
      JSON.stringify({
        verified_at: '2026-09-09T00:00:00.000Z',
        customer_id: '',
        entitlements: [],
      }),
      {
        status: 200,
        headers: { 'content-type': 'application/json' },
      },
    );
  });

  try {
    const client = new IapStackClient(clientConfig());
    await assert.rejects(
      () => client.verifyPurchase(samplePurchase),
      (error) => error instanceof IapStackProtocolError,
    );
  } finally {
    restore();
  }
});

test('fails closed when an allowed entitlement reaches its effective end', () => {
  const endsAt = new Date('2026-08-26T12:00:00.000Z');
  const entitlement = new Entitlement({
    key: 'premium',
    access: 'allowed',
    reason: 'canceled_at_period_end',
    version: 7,
    effectiveStartsAt: new Date('2026-07-27T12:00:00.000Z'),
    effectiveEndsAt: endsAt,
  });

  assert.equal(entitlement.grantsAccessAt(new Date(endsAt.getTime() - 1)), true);
  assert.equal(entitlement.grantsAccessAt(endsAt), false);
  assert.equal(entitlement.grantsAccessAt(new Date(endsAt.getTime() + 3600_000)), false);
  assert.equal(
    new Entitlement({
      key: 'premium',
      access: 'denied',
      reason: 'expired',
      version: 1,
    }).grantsAccess,
    false,
  );
});

test('evidence helper builders validate required input', () => {
  const apple = appleEvidence({
    signedTransaction: 'aaa.bbb.ccc',
    productKind: 'subscription',
  });
  assert.equal(apple.product_kind, 'subscription');
  assert.equal(apple.signed_transaction, 'aaa.bbb.ccc');

  const google = googlePlayEvidence({
    purchaseToken: 'play-token',
    productKind: 'non_consumable',
  });
  assert.equal(google.product_kind, 'non_consumable');

  const huawei = huaweiEvidence({
    purchaseData: '{"ok":true}',
    signature: 'sig',
    productKind: 'subscription',
  });
  assert.equal(huawei.signature, 'sig');
  assert.equal(huawei.purchase_data, '{"ok":true}');

  assert.throws(() => {
    googlePlayEvidence({ purchaseToken: 'x' } as never);
  });
  assert.throws(() => {
    appleEvidence({ signedTransaction: 'txn', productKind: 'subscription' });
  });
  assert.throws(() => {
    appleEvidence({ signedTransaction: '   ', productKind: 'subscription' });
  });
  assert.throws(() => {
    huaweiEvidence({
      purchaseData: 'not-json',
      signature: 'sig',
      productKind: 'subscription',
    });
  });
});
