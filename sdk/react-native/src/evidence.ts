import {
  AppleEvidence,
  GooglePlayEvidence,
  HuaweiEvidence,
  ProductKind,
} from './models';

interface AppleInput {
  signedTransaction?: string;
  signed_transaction?: string;
  productKind?: ProductKind;
  product_kind?: ProductKind;
}

interface GooglePlayInput {
  purchaseToken?: string;
  purchase_token?: string;
  productKind?: ProductKind;
  product_kind?: ProductKind;
}

interface HuaweiInput {
  purchaseData?: string;
  purchase_data?: string;
  signature?: string;
  productKind?: ProductKind;
  product_kind?: ProductKind;
}

function readProductKind(kind: unknown): ProductKind {
  if (kind === 'subscription' || kind === 'non_consumable') {
    return kind;
  }
  throw new Error('product kind must be subscription or non_consumable');
}

function requireNonEmpty(value: unknown, message: string): string {
  if (typeof value !== 'string' || value.trim() === '') {
    throw new Error(message);
  }
  return value;
}

export function appleEvidence(input: AppleInput): AppleEvidence {
  const signedTransaction = requireNonEmpty(
    input.signedTransaction ?? input.signed_transaction,
    'apple evidence requires signed_transaction',
  );
  if (signedTransaction.split('.').length !== 3) {
    throw new Error('apple evidence requires a compact JWS signed_transaction');
  }
  return {
    signed_transaction: signedTransaction,
    product_kind: readProductKind(input.productKind ?? input.product_kind),
  };
}

export function googlePlayEvidence(input: GooglePlayInput): GooglePlayEvidence {
  const purchaseToken = requireNonEmpty(
    input.purchaseToken ?? input.purchase_token,
    'google play evidence requires purchase_token',
  );
  return {
    purchase_token: purchaseToken,
    product_kind: readProductKind(input.productKind ?? input.product_kind),
  };
}

export function huaweiEvidence(input: HuaweiInput): HuaweiEvidence {
  const purchaseData = requireNonEmpty(
    input.purchaseData ?? input.purchase_data,
    'huawei evidence requires purchase_data and signature',
  );
  const signature = requireNonEmpty(
    input.signature,
    'huawei evidence requires purchase_data and signature',
  );
  let decoded: unknown;
  try {
    decoded = JSON.parse(purchaseData);
  } catch {
    throw new Error('huawei purchase data is not a valid JSON object');
  }
  if (!decoded || typeof decoded !== 'object' || Array.isArray(decoded)) {
    throw new Error('huawei purchase data is not a valid JSON object');
  }
  return {
    purchase_data: purchaseData,
    signature,
    product_kind: readProductKind(input.productKind ?? input.product_kind),
  };
}
