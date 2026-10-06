import {NativeEventEmitter, NativeModules, Platform} from 'react-native';
import {IapStackConfig} from './config';
import {IapStackProtocolError} from './errors';
import {
  decodeEntitlementSnapshot, decodeRestoreResult, decodeVerificationResult,
  type EntitlementSnapshot, type ProductKind, type RestoreResult, type VerificationResult,
} from './models';

export type Storefront = 'apple' | 'google_play' | 'huawei';
/** Display metadata only. Signed purchase evidence never passes through JavaScript. */
export interface StoreProduct {
  id: string;
  selectionKey: string;
  kind: ProductKind;
  title: string;
  description: string;
  price: string;
  currencyCode: string;
  basePlanId?: string;
  offerId?: string;
  offerTags?: string[];
  pricingPhases?: {billingCycleCount: number; billingPeriod: string; formattedPrice: string;
    priceMicros: string; currencyCode: string; recurrence: string}[];
  subscriptionPeriod?: string;
  freeTrialPeriod?: string;
}
export interface StoreProductQuery {
  products: StoreProduct[];
  notFoundProductIds: string[];
}
export interface StoreConfiguration {
  storefront: Storefront;
  baseUri: string;
  applicationId: string;
  /** Short-lived token obtained from your authenticated trusted host, kept only in memory. */
  customerToken: string;
  externalCustomerId: string;
  productKinds: Record<string, ProductKind>;
  allowInsecureHttp?: boolean;
}
export type StoreEvent =
  | {type: 'verified'; result: VerificationResult}
  | {type: 'error'; code: string; message: string};

export interface CustomerSession {
  baseUri: string;
  applicationId: string;
  customerToken: string;
  externalCustomerId: string;
  expiresAt: Date;
}

interface NativeStoreModule {
  requestSession(endpoint: string, loginToken: string): Promise<Record<string, unknown>>;
  configure(config: StoreConfiguration): Promise<void>;
  isAvailable(): Promise<boolean>;
  resolveHuaweiEnvironment(): Promise<boolean>;
  queryProducts(productIds: string[]): Promise<StoreProductQuery>;
  purchase(selectionKey: string, requestId: string | null): Promise<Record<string, unknown> | null>;
  restore(requestId: string | null): Promise<Record<string, unknown>>;
  getEntitlements(requestId: string | null): Promise<Record<string, unknown>>;
  dispose(): Promise<void>;
  addListener(event: string): void;
  removeListeners(count: number): void;
}
function nativeModule(): NativeStoreModule {
  const module = NativeModules.IAPStackStore as NativeStoreModule | undefined;
  if (!module) throw new Error('IAPStackStore is unavailable. Install native dependencies and rebuild the app.');
  return module;
}
function decode<T>(fn: () => T): T {
  try { return fn(); } catch (error) {
    throw new IapStackProtocolError('Native IAPStack response did not match the v1 contract', error);
  }
}

/** One native session per process. Dispose before switching or renewing customer sessions. */
export const IapStackStore = {
  /** Optional trusted-host bootstrap for the documented /session contract. Never pass an application bearer. */
  async requestCustomerSession(endpoint: string, loginToken: string): Promise<CustomerSession> {
    const url = new URL(endpoint);
    if (url.protocol !== 'https:' || !url.host || url.username || url.password || url.search || url.hash) {
      throw new TypeError('Session endpoint must be a direct HTTPS URL without credentials, query, or fragment');
    }
    if (!loginToken || /\s/.test(loginToken)) throw new TypeError('loginToken must be a non-empty bearer without spaces');
    const raw = await nativeModule().requestSession(endpoint, loginToken);
    return decode(() => {
      for (const key of ['base_url', 'application_id', 'token', 'external_customer_id', 'expires_at']) {
        if (typeof raw[key] !== 'string' || !raw[key]) throw new TypeError('Invalid customer session');
      }
      const config = new IapStackConfig({baseUri: raw.base_url as string,
        applicationId: raw.application_id as string, customerToken: raw.token as string});
      const customer = raw.external_customer_id as string;
      const expiresAt = new Date(raw.expires_at as string);
      if (customer.trim() !== customer || !Number.isFinite(expiresAt.getTime()) || expiresAt.getTime() <= Date.now()) {
        throw new TypeError('Invalid or expired customer session');
      }
      return {baseUri: config.baseUri, applicationId: config.applicationId, customerToken: config.customerToken,
        externalCustomerId: customer, expiresAt};
    });
  },
  async configure(config: StoreConfiguration): Promise<void> {
    if ((Platform.OS === 'ios' && config.storefront !== 'apple') ||
        (Platform.OS === 'android' && config.storefront === 'apple') ||
        !['ios', 'android'].includes(Platform.OS)) {
      throw new Error(`${config.storefront} is unsupported on ${Platform.OS}`);
    }
    if (!['apple', 'google_play', 'huawei'].includes(config.storefront)) {
      throw new Error('Unknown storefront');
    }
    new IapStackConfig(config);
    if (!config.externalCustomerId || config.externalCustomerId.trim() !== config.externalCustomerId) {
      throw new TypeError('externalCustomerId must be non-empty without surrounding whitespace');
    }
    if (config.storefront === 'apple' &&
        !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/u.test(config.externalCustomerId)) {
      throw new TypeError('Apple externalCustomerId must be a lowercase UUID');
    }
    if (!config.productKinds || Object.keys(config.productKinds).length === 0 ||
        Object.entries(config.productKinds).some(([id, kind]) => !id || id.trim() !== id ||
          (kind !== 'subscription' && kind !== 'non_consumable'))) {
      throw new TypeError('productKinds must contain non-empty product IDs and supported kinds');
    }
    await nativeModule().configure({...config, productKinds: {...config.productKinds}});
  },
  isAvailable(): Promise<boolean> { return nativeModule().isAvailable(); },
  /** Call only from a user action after HMS reports hms_sign_in_required. */
  resolveHuaweiEnvironment(): Promise<boolean> { return nativeModule().resolveHuaweiEnvironment(); },
  queryProducts(productIds: string[]): Promise<StoreProductQuery> {
    if (!productIds.length || productIds.some(id => !id || id.trim() !== id)) {
      return Promise.reject(new TypeError('productIds must contain non-empty IDs'));
    }
    return nativeModule().queryProducts(productIds);
  },
  /** Apple/Huawei return verification. Play returns null after opening checkout; listen for verified/error events. */
  async purchase(selectionKey: string, requestId?: string): Promise<VerificationResult | null> {
    const result = await nativeModule().purchase(selectionKey, requestId ?? null);
    return result === null ? null : decode(() => decodeVerificationResult(result));
  },
  async restore(requestId?: string): Promise<RestoreResult> {
    const result = await nativeModule().restore(requestId ?? null);
    return decode(() => decodeRestoreResult(result));
  },
  async getEntitlements(requestId?: string): Promise<EntitlementSnapshot> {
    const result = await nativeModule().getEntitlements(requestId ?? null);
    return decode(() => decodeEntitlementSnapshot(result));
  },
  /** Subscribe before configure; updates are verified natively even when JS has no listener. */
  addListener(listener: (event: StoreEvent) => void): {remove(): void} {
    return new NativeEventEmitter(nativeModule()).addListener('IAPStackStoreUpdate', (event: {
      type: string; result: Record<string, unknown>; code: string; message: string;
    }) => {
      if (event.type === 'verified') {
        let result: VerificationResult;
        try { result = decode(() => decodeVerificationResult(event.result)); }
        catch {
          listener({type: 'error', code: 'protocol_error', message: 'Invalid native verification result'});
          return;
        }
        // App callback failures are app errors, not malformed native responses.
        listener({type: 'verified', result});
      } else if (event.type === 'error') {
        listener({type: 'error', code: event.code, message: event.message});
      }
    });
  },
  /** Cancels native work and releases the customer bearer. Restore after interrupted checkout. */
  dispose(): Promise<void> { return nativeModule().dispose(); },
};
