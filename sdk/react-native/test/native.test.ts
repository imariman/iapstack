import {beforeEach, expect, mock, test} from 'bun:test';
const platform = {OS: 'ios'};
const calls: {operation: string; args: unknown[]}[] = [];
const wire = {customer_id: 'internal-id', verified_at: '2026-10-06T12:00:00Z',
  entitlements: [{key: 'premium', access: 'allowed', reason: 'active', version: 1}]};
let callback: (event: unknown) => void;
const sessionWire = {base_url: 'https://iapstack.example.com', application_id: 'app', token: 'short-lived-token',
  external_customer_id: '00000000-0000-4000-8000-000000000001', expires_at: '2999-01-01T00:00:00Z'};
let sessionResponse: Record<string, unknown> = sessionWire;
let restoreImpl: () => Promise<Record<string, unknown>> = async () => ({results: [wire]});
const native = {
  requestSession: async (...args: unknown[]) => { calls.push({operation: 'session', args}); return sessionResponse; },
  configure: async (...args: unknown[]) => { calls.push({operation: 'configure', args}); },
  purchase: async (...args: unknown[]) => { calls.push({operation: 'purchase', args}); return wire; },
  restore: async () => restoreImpl(),
  getEntitlements: async () => wire,
  dispose: async () => { calls.push({operation: 'dispose', args: []}); },
  httpRequest: async (...args: unknown[]) => { calls.push({operation: 'http', args}); return wire; },
};
mock.module('react-native', () => ({Platform: platform, NativeModules: {IAPStackStore: native},
  NativeEventEmitter: class {addListener(_name: string, listener: (event: unknown) => void) {
    callback = listener; return {remove() {}};
  }}}));
const {IapStackStore} = await import('../src/native');
const {IapStackIncompleteRestoreError} = await import('../src/errors');
const {NativeIapStackClient} = await import('../src/native_client');
const {IapStackConfig} = await import('../src/config');
const config = {storefront: 'apple' as const, baseUri: 'https://iapstack.example.com', applicationId: 'app',
  customerToken: 'short-lived-token', externalCustomerId: '00000000-0000-4000-8000-000000000001',
  productKinds: {premium: 'subscription' as const}};
beforeEach(() => { calls.length = 0; platform.OS = 'ios'; sessionResponse = sessionWire;
  restoreImpl = async () => ({results: [wire]}); });
test('customer session bootstrap validates endpoint before sending credentials', async () => {
  for (const endpoint of ['http://host.test/session', 'https://user:pass@host.test/session', 'https://host.test/session?secret=1']) {
    await expect(IapStackStore.requestCustomerSession(endpoint, 'login-token')).rejects.toThrow('HTTPS');
  }
  await expect(IapStackStore.requestCustomerSession('https://host.test/session', 'with spaces')).rejects.toThrow('loginToken');
  expect(calls).toHaveLength(0);
});
test('customer session bootstrap returns validated configuration with expiry', async () => {
  const session = await IapStackStore.requestCustomerSession('https://host.test/session', 'login-token');
  expect(session.customerToken).toBe('short-lived-token');
  expect(session.expiresAt).toBeInstanceOf(Date);
  expect(calls[0]).toEqual({operation: 'session', args: ['https://host.test/session', 'login-token']});
});
test('customer session bootstrap rejects expired, malformed, or insecure sessions', async () => {
  for (const invalid of [{expires_at: '2000-01-01T00:00:00Z'}, {expires_at: 'invalid'}, {token: 123},
    {base_url: 'http://insecure.test'}, {external_customer_id: ' surrounded '}]) {
    sessionResponse = {...sessionWire, ...invalid};
    await expect(IapStackStore.requestCustomerSession('https://host.test/session', 'login-token')).rejects.toThrow('contract');
  }
});
test('Huawei and Google Play fail closed on iOS before touching native module', async () => {
  for (const storefront of ['huawei', 'google_play'] as const) {
    await expect(IapStackStore.configure({...config, storefront})).rejects.toThrow('unsupported');
  }
  expect(calls).toHaveLength(0);
});
test('Apple rejects Android and invalid customer binding', async () => {
  platform.OS = 'android';
  await expect(IapStackStore.configure(config)).rejects.toThrow('unsupported');
  platform.OS = 'ios';
  await expect(IapStackStore.configure({...config, externalCustomerId: 'not-a-uuid'})).rejects.toThrow('UUID');
  expect(calls).toHaveLength(0);
});
test('configure rejects malformed kinds and passes an isolated catalog snapshot', async () => {
  await expect(IapStackStore.configure({...config, productKinds: {}})).rejects.toThrow('productKinds');
  await IapStackStore.configure(config);
  const passed = calls[0].args[0] as typeof config;
  expect(passed).toEqual(config);
  expect(passed.productKinds).not.toBe(config.productKinds);
});
test('native verified results become public entitlement and Date models', async () => {
  const result = await IapStackStore.purchase('premium');
  expect(result!.verifiedAt).toBeInstanceOf(Date);
  expect(result!.entitlements[0].grantsAccess).toBe(true);
  expect((await IapStackStore.restore()).results[0].entitlements[0].key).toBe('premium');
  expect((await IapStackStore.getEntitlements()).customerId).toBe('internal-id');
  expect(calls[0]).toEqual({operation: 'purchase', args: ['premium', null]});
});
test('pending purchase updates stay distinct from verified access', () => {
  const received: Array<Record<string, unknown>> = [];
  const listener = IapStackStore.addListener(event => received.push({...event}));
  callback({type: 'pending', productIds: ['premium', 1]});
  callback({type: 'pending', result: {customer_id: 'internal-id', verified_at: '2026-10-06T12:00:00Z',
    entitlements: [{key: 'premium', access: 'unresolved', reason: 'pending_payment', version: 1}]}});
  callback({type: 'pending', result: {entitlements: 'invalid'}});
  // #252: iOS emits pending (not verified) for an empty snapshot or one mixing an
  // allowed entitlement with a pending_payment one; JS must keep it pending.
  callback({type: 'pending', result: {customer_id: 'internal-id', verified_at: '2026-10-06T12:00:00Z',
    entitlements: []}});
  callback({type: 'pending', result: {customer_id: 'internal-id', verified_at: '2026-10-06T12:00:00Z',
    entitlements: [{key: 'premium', access: 'allowed', reason: 'active', version: 1},
      {key: 'extra', access: 'unresolved', reason: 'pending_payment', version: 1}]}});
  expect(received[0]).toEqual({type: 'pending', productIds: ['premium']});
  const pendingResult = received[1]?.result as {entitlements: {grantsAccess: boolean}[]};
  expect(received[1]?.type).toBe('pending');
  expect(pendingResult.entitlements[0]?.grantsAccess).toBe(false);
  expect(received[2]).toEqual({type: 'error', code: 'protocol_error', message: 'Invalid native verification result'});
  expect(received[3]?.type).toBe('pending');
  expect((received[3]?.result as {entitlements: unknown[]}).entitlements).toHaveLength(0);
  expect(received[4]?.type).toBe('pending');
  expect((received[4]?.result as {entitlements: {grantsAccess: boolean}[]}).entitlements[1].grantsAccess).toBe(false);
  listener.remove();
});
test('purchase updates decode and malformed native payloads fail closed', () => {
  const received: unknown[] = [];
  const listener = IapStackStore.addListener(event => received.push(event));
  callback({type: 'verified', result: wire});
  callback({type: 'verified', result: {entitlements: 'invalid'}});
  expect((received[0] as {result: {entitlements: unknown[]}}).result.entitlements).toHaveLength(1);
  expect(received[1]).toEqual({type: 'error', code: 'protocol_error', message: 'Invalid native verification result'});
  listener.remove();
});
test('RN HTTP uses native clients and preserves signed evidence strings byte-for-byte', async () => {
  const client = new NativeIapStackClient(new IapStackConfig(config));
  const signed = ' { "productId": "premium", "space": "é 😀" }\n';
  const result = await client.verifyPurchase({externalCustomerId: config.externalCustomerId,
    claimedProducts: ['premium'], evidence: {purchase_data: signed, signature: ' exact signature '}});
  expect(result.entitlements[0].grantsAccess).toBe(true);
  const args = calls[0].args;
  expect(args[1]).toBe('purchases:verify');
  expect((args[3] as {evidence: {purchase_data: string}}).evidence.purchase_data).toBe(signed);
});
test('listener exceptions propagate once without emitting a false protocol error', () => {
  const appError = new Error('App callback failed');
  let calls = 0;
  const listener = IapStackStore.addListener(() => { calls++; throw appError; });
  expect(() => callback({type: 'verified', result: wire})).toThrow(appError);
  expect(calls).toBe(1);
  listener.remove();
});
test('restore surfaces skip counts and the Huawei page cap', async () => {
  restoreImpl = async () => ({results: [wire], pageLimitReached: true,
    skipped: {unbound: 1, differentCustomer: 2, unknownProduct: 3, multipleProducts: 4}});
  const restored = await IapStackStore.restore();
  expect(restored.results[0].entitlements[0].key).toBe('premium');
  expect(restored.pageLimitReached).toBe(true);
  expect(restored.skipped).toEqual({unbound: 1, differentCustomer: 2, unknownProduct: 3,
    multipleProducts: 4, total: 10});
});
test('restore defaults skip counts and page cap for older native builds', async () => {
  const restored = await IapStackStore.restore();
  expect(restored.pageLimitReached).toBe(false);
  expect(restored.skipped).toEqual({unbound: 0, differentCustomer: 0, unknownProduct: 0,
    multipleProducts: 0, total: 0});
});
test('restore rejects on malformed skip metadata instead of guessing counts', async () => {
  restoreImpl = async () => ({results: [wire], skipped: {differentCustomer: -1}});
  await expect(IapStackStore.restore()).rejects.toThrow('contract');
  restoreImpl = async () => ({results: [wire], pageLimitReached: 'yes'});
  await expect(IapStackStore.restore()).rejects.toThrow('contract');
});
test('a later restore batch failure rejects with the committed first batch attached', async () => {
  const nativeError = {code: 'service_unavailable', message: 'IAPStack operation failed (service_unavailable)',
    userInfo: {statusCode: 503, retryable: true, retryAfterMs: 30000, requestId: 'req-2',
      committedResults: [wire], pageLimitReached: true, skipped: {differentCustomer: 1}}};
  restoreImpl = async () => { throw nativeError; };
  let thrown: unknown;
  try { await IapStackStore.restore(); } catch (error) { thrown = error; }
  expect(thrown).toBeInstanceOf(IapStackIncompleteRestoreError);
  const incomplete = thrown as InstanceType<typeof IapStackIncompleteRestoreError>;
  expect(incomplete.code).toBe('service_unavailable');
  expect(incomplete.committedResults).toHaveLength(1);
  expect(incomplete.committedResults[0].entitlements[0].grantsAccess).toBe(true);
  expect(incomplete.skipped.differentCustomer).toBe(1);
  expect(incomplete.pageLimitReached).toBe(true);
  expect(incomplete.cause).toBe(nativeError);
  // The API failure's retry metadata stays readable on the typed error.
  expect(incomplete.statusCode).toBe(503);
  expect(incomplete.retryable).toBe(true);
  expect(incomplete.retryAfterMs).toBe(30000);
  expect(incomplete.requestId).toBe('req-2');
});
test('an incomplete restore without an API failure leaves the API fields unset', async () => {
  // iOS: every batch was accepted, then StoreKit could not finish a transaction.
  restoreImpl = async () => { throw {code: 'apple_complete_failed', message: 'IAPStack operation failed (apple_complete_failed)',
    userInfo: {committedResults: [wire], skipped: {}, pageLimitReached: false}}; };
  let thrown: unknown;
  try { await IapStackStore.restore(); } catch (error) { thrown = error; }
  const incomplete = thrown as InstanceType<typeof IapStackIncompleteRestoreError>;
  expect(incomplete).toBeInstanceOf(IapStackIncompleteRestoreError);
  expect(incomplete.code).toBe('apple_complete_failed');
  expect(incomplete.statusCode).toBeUndefined();
  expect(incomplete.retryable).toBeUndefined();
  expect(incomplete.retryAfterMs).toBeUndefined();
  expect(incomplete.requestId).toBeUndefined();
});
test('restore failures without committed batches keep the plain native rejection', async () => {
  for (const nativeError of [{code: 'transport_error', message: 'IAPStack operation failed (transport_error)'},
    {code: 'transport_error', message: 'offline', userInfo: {committedResults: []}},
    {code: 'transport_error', message: 'offline', userInfo: {committedResults: [{entitlements: 'invalid'}]}},
    // Restore progress decodes with the success-path rules: malformed metadata
    // is a contract violation there, so it never becomes a typed error here.
    {code: 'transport_error', message: 'offline', userInfo: {committedResults: [wire], pageLimitReached: 'yes'}},
    {code: 'transport_error', message: 'offline', userInfo: {committedResults: [wire], pageLimitReached: 1}},
    {code: 'transport_error', message: 'offline', userInfo: {committedResults: [wire], skipped: {unbound: 1.5}}}]) {
    restoreImpl = async () => { throw nativeError; };
    let thrown: unknown;
    try { await IapStackStore.restore(); } catch (error) { thrown = error; }
    expect(thrown).toBe(nativeError);
  }
});
test('dispose delegates credential release to native session', async () => {
  await IapStackStore.dispose(); expect(calls[0].operation).toBe('dispose');
});
