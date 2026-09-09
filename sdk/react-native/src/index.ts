export interface IapStackReactNativeConfig {
  apiBaseUrl: string;
  applicationId: string;
  customerSessionToken: string;
}

export interface EntitlementSnapshot {
  entitlementId: string;
  productId: string;
  granted: boolean;
  expiresAt: string | null;
}

export class NotImplementedError extends Error {
  constructor(feature: string) {
    super(`${feature} is not implemented yet (issue #88 placeholder)`);
    this.name = "NotImplementedError";
  }
}

export class IapStackClient {
  constructor(private readonly config: IapStackReactNativeConfig) {}

  async getEntitlements(_customerId: string): Promise<EntitlementSnapshot[]> {
    throw new NotImplementedError("getEntitlements");
  }

  async acknowledgePurchase(_purchaseToken: string): Promise<void> {
    throw new NotImplementedError("acknowledgePurchase");
  }
}

export function createClient(config: IapStackReactNativeConfig): IapStackClient {
  return new IapStackClient(config);
}
