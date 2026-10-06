import {NativeModules} from 'react-native';
import {IapStackClient} from './client';
import {IapStackConfig} from './config';
import {IapStackApiError, IapStackProtocolError, IapStackTimeoutError, IapStackTransportError} from './errors';

/** RN fetch cannot reliably disable redirects. Use the canonical native HTTP clients on mobile. */
export class NativeIapStackClient extends IapStackClient {
  constructor(private readonly nativeConfig: IapStackConfig) { super(nativeConfig); }
  protected override async request(_method: 'GET' | 'POST', path: string[],
    params: {requestId?: string; body?: Record<string, unknown>} = {}): Promise<Record<string, unknown>> {
    const native = NativeModules.IAPStackStore;
    if (!native) throw new Error('IAPStack native module is unavailable; install native dependencies and rebuild');
    try {
      return await native.httpRequest({...this.nativeConfig, retryPolicy: {...this.nativeConfig.retryPolicy}},
        path[path.length - 1], path[path.length - 2], params.body ?? {}, params.requestId ?? null);
    } catch (error) {
      const failure = error as {code?: string; message?: string; userInfo?: {statusCode?: number; requestId?: string; retryable?: boolean; retryAfterMs?: number}};
      const info = failure.userInfo;
      const message = failure.message ?? 'Native IAPStack request failed';
      if (info?.statusCode) throw new IapStackApiError(info.statusCode, failure.code ?? 'http_error',
        message, info.retryable ?? false, info.requestId, info.retryAfterMs);
      if (failure.code === 'protocol_error') throw new IapStackProtocolError(message);
      if (failure.code === 'timeout') throw new IapStackTimeoutError(message);
      throw new IapStackTransportError(message);
    }
  }
}
