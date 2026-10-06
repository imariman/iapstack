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
  MAX_RETRY_AFTER_MS,
} from '../src/index';
import { parseRetryAfter } from '../src/retry_after';

type MockRequest = {
  url: string;
  init: RequestInit;
};

function withMockFetch(
  handler: (input: RequestInfo | URL, init: RequestInit) => Promise<Response>,
): { calls: MockRequest[]; restore: () => void } {
  const originalFetch = globalThis.fetch;
  const calls: MockRequest[] = [];
  globalThis.fetch = (async (input: RequestInfo | URL, init: RequestInit = {}) => {
    calls.push({
      url: input instanceof URL ? input.toString() : `${input}`,
      init,
    });
    return handler(input, init);
  }) as typeof fetch;

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
    assert.equal(headers.get('X-IAPStack-SDK'), 'react-native/0.1.0-sdk.1');
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

test('restorePurchases rejects incomplete or extra results without retrying', async () => {
  for (const count of [0, 2]) {
    const {calls, restore} = withMockFetch(async () => new Response(JSON.stringify({
      results: Array.from({length: count}, verificationPayload),
    }), {status: 200}));
    try {
      const client = new IapStackClient(clientConfig());
      await assert.rejects(() => client.restorePurchases([samplePurchase]), IapStackProtocolError);
      assert.equal(calls.length, 1);
    } finally { restore(); }
  }
});

test('API errors expose the Retry-After cooldown', async () => {
  const { restore } = withMockFetch(
    async () =>
      new Response(JSON.stringify({ error: { code: 'rate_limited', message: 'slow down' } }), {
        status: 429,
        headers: { 'content-type': 'application/json', 'retry-after': '17' },
      }),
  );

  try {
    const client = new IapStackClient(
      clientConfig({ retryPolicy: new IapStackRetryPolicy({ maxAttempts: 1 }) }),
    );
    await assert.rejects(
      () => client.getEntitlements('customer-123'),
      (error) => {
        assert(error instanceof IapStackApiError);
        assert.equal(error.retryable, true);
        assert.equal(error.retryAfterMs, 17_000);
        return true;
      },
    );
  } finally {
    restore();
  }
});

test('an elapsed Retry-After does not block the retry', async () => {
  let attempt = 0;
  const { restore } = withMockFetch(async () => {
    attempt += 1;
    if (attempt === 1) {
      return new Response(JSON.stringify({ error: { code: 'provider_unavailable', message: 'x' } }), {
        status: 503,
        headers: { 'content-type': 'application/json', 'retry-after': '0' },
      });
    }
    return new Response(JSON.stringify(restorePayload()), {
      status: 200,
      headers: { 'content-type': 'application/json' },
    });
  });

  try {
    const client = new IapStackClient(
      clientConfig({
        retryPolicy: new IapStackRetryPolicy({ maxAttempts: 2, baseDelayMs: 0, maxDelayMs: 0 }),
      }),
    );
    const result = await client.restorePurchases([samplePurchase]);
    assert.equal(attempt, 2);
    assert.equal(result.results[0].customerId, 'customer-123');
  } finally {
    restore();
  }
});

test('parseRetryAfter accepts delay-seconds and round-tripping IMF-fixdates only', () => {
  const now = Date.UTC(2026, 8, 28, 12, 0, 0);
  const cases: Array<[string | null | undefined, number | undefined]> = [
    [undefined, undefined],
    [null, undefined],
    ['', undefined],
    ['17', 17_000],
    [' 5 ', 5_000],
    ['999999999', 999_999_999_000],
    ['1000000000', undefined],
    ['Fri, 02 Oct 2026 00:00:00 GMT', Date.UTC(2026, 9, 2) - now],
    ['Mon, 28 Sep 2026 11:59:00 GMT', 0],
    ['Fri, 31 Feb 2026 00:00:00 GMT', undefined],
    ['Wed, 31 Apr 2026 00:00:00 GMT', undefined],
    ['Mon, 02 Oct 2026 00:00:00 GMT', undefined],
    ['Friday, 02-Oct-26 00:00:00 GMT', undefined],
    ['Fri Oct  2 00:00:00 2026', undefined],
    ['Fri, 02 Oct 2026 00:00:00 +0000', undefined],
    ['02 Oct 2026 00:00:00 GMT', undefined],
    ['Fri, 02 Oct 2026 24:00:00 GMT', undefined],
    ['-3', undefined],
    ['1.5', undefined],
    ['March 1, 2026', undefined],
    ['soon', undefined],
  ];
  for (const [header, expected] of cases) {
    assert.equal(parseRetryAfter(header, now), expected, `Retry-After ${String(header)}`);
  }
});

test('retry policy raises the jitter delay to a budgeted Retry-After', () => {
  const policy = new IapStackRetryPolicy({ baseDelayMs: 100, maxDelayMs: 250 });
  assert.equal(policy.delayAfter(1, 0.5), 50);
  assert.equal(policy.delayAfter(1, 0.5, 0), 50);
  assert.equal(policy.delayAfter(1, 1, 20), 100);
  assert.equal(policy.delayAfter(1, 0, 4000), 4000);
  assert.equal(policy.delayAfter(1, 1, 3_600_000), MAX_RETRY_AFTER_MS);
  assert.equal(MAX_RETRY_AFTER_MS, 30_000);
  assert.throws(() => policy.delayAfter(1, 1.1, 4000));

  // The budget bounds the cooldown, never the configured jitter.
  const wide = new IapStackRetryPolicy({ baseDelayMs: 40_000, maxDelayMs: 60_000 });
  assert.equal(wide.delayAfter(1, 1, 1000), 40_000);
});

test('client waits at least the Retry-After cooldown before retrying', async () => {
  let attempt = 0;
  const { restore } = withMockFetch(async () => {
    attempt += 1;
    if (attempt === 1) {
      return new Response(JSON.stringify({ error: { code: 'provider_unavailable', message: 'x' } }), {
        status: 503,
        headers: { 'content-type': 'application/json', 'retry-after': '1' },
      });
    }
    return new Response(JSON.stringify(restorePayload()), {
      status: 200,
      headers: { 'content-type': 'application/json' },
    });
  });

  try {
    const client = new IapStackClient(
      clientConfig({
        retryPolicy: new IapStackRetryPolicy({ maxAttempts: 2, baseDelayMs: 0, maxDelayMs: 0 }),
      }),
    );
    const startedAt = Date.now();
    await client.restorePurchases([samplePurchase]);
    assert.equal(attempt, 2);
    assert.ok(Date.now() - startedAt >= 1000, 'the client must sleep for the Retry-After second');
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

test('redirects are surfaced as non-retryable API errors without being followed', async () => {
  const { calls, restore } = withMockFetch(async () =>
    new Response(null, {
      status: 307,
      headers: { location: 'https://elsewhere.test/collect' },
    }),
  );

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
        assert.equal((error as IapStackApiError).statusCode, 307);
        assert.equal((error as IapStackApiError).retryable, false);
        return true;
      },
    );
    assert.equal(calls.length, 1);
    assert.equal(calls[0].init.redirect, 'manual');
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

function verificationWithTimestamps(verifiedAt: string, effectiveEndsAt: string | null) {
  const payload = verificationPayload();
  return {
    ...payload,
    verified_at: verifiedAt,
    entitlements: [{ ...payload.entitlements[0], effective_ends_at: effectiveEndsAt }],
  };
}

test('rejects non-RFC 3339 and impossible timestamps', async () => {
  const valid = '2026-09-09T00:00:00.000Z';
  const invalid = [
    'March 1, 2026',
    // `new Date` rolls these into a later instant instead of failing.
    '2026-02-31T00:00:00Z',
    '2026-01-01T24:00:00Z',
    '2027-02-29T00:00:00Z',
    '2026-04-31T10:00:00-05:30',
    '2026-01-01T00:00:00+23:60',
  ];
  // verified_at uses the required parser and effective_ends_at the optional one.
  const payloads = invalid.flatMap((raw) => [
    verificationWithTimestamps(raw, null),
    verificationWithTimestamps(valid, raw),
  ]);
  for (const payload of payloads) {
    const { restore } = withMockFetch(
      async () =>
        new Response(JSON.stringify(payload), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        }),
    );
    try {
      const client = new IapStackClient(clientConfig());
      await assert.rejects(
        () => client.verifyPurchase(samplePurchase),
        (error) => error instanceof IapStackProtocolError,
        JSON.stringify(payload),
      );
    } finally {
      restore();
    }
  }
});

test('accepts leap days and numeric offsets', async () => {
  const { restore } = withMockFetch(
    async () =>
      new Response(
        JSON.stringify(
          verificationWithTimestamps('2028-02-29T00:00:00Z', '2026-12-31T23:59:59.5+05:30'),
        ),
        { status: 200, headers: { 'content-type': 'application/json' } },
      ),
  );
  try {
    const client = new IapStackClient(clientConfig());
    const result = await client.verifyPurchase(samplePurchase);
    assert.equal(result.verifiedAt.toISOString(), '2028-02-29T00:00:00.000Z');
    assert.equal(
      result.entitlements[0]?.effectiveEndsAt?.toISOString(),
      '2026-12-31T18:29:59.500Z',
    );
  } finally {
    restore();
  }
});

test('rejects external customer IDs with surrounding whitespace', async () => {
  const { calls, restore } = withMockFetch(async () => new Response('{}', { status: 200 }));
  try {
    const client = new IapStackClient(clientConfig());
    await assert.rejects(() => client.getEntitlements(' customer-123 '), TypeError);
    await assert.rejects(
      () => client.verifyPurchase({ ...samplePurchase, externalCustomerId: 'customer-123 ' }),
      TypeError,
    );
    assert.equal(calls.length, 0);
  } finally {
    restore();
  }
});

test('configuration rejects blank application IDs and non-finite limits', () => {
  const base = {baseUri: 'https://example.com', applicationId: 'app', customerToken: 'token'};
  for (const override of [{applicationId: ' '}, {timeoutMs: NaN}, {timeoutMs: Infinity},
    {maxResponseBytes: NaN}, {maxResponseBytes: 1.5}]) {
    assert.throws(() => new IapStackConfig({...base, ...override}));
  }
  assert.throws(() => new IapStackRetryPolicy({baseDelayMs: NaN}));
});
