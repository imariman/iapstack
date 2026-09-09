import { AppleEvidence, GooglePlayEvidence, HuaweiEvidence, ProductKind } from './models';

const VALID_PRODUCT_KINDS = ['subscription', 'non_consumable'] as const;

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

function readProductKind(kind?: string): ProductKind {
  if (kind === 'subscription' || kind === 'non_consumable') {
    return kind;
  }
  throw new Error('product kind must be subscription or non_consumable');
}

export function appleEvidence(input: AppleInput): AppleEvidence {
  const signedTransaction = input.signedTransaction ?? input.signed_transaction;
  if (!signedTransaction) {
    throw new Error('apple evidence requires signed_transaction');
  }
  const productKind = readProductKind(
    input.productKind ?? input.product_kind ?? 'non_consumable',
  );
  if (!VALID_PRODUCT_KINDS.includes(productKind)) {
    throw new Error('unsupported product kind');
  }
  return {
    signed_transaction: signedTransaction,
    product_kind: productKind,
  };
}

export function googlePlayEvidence(input: GooglePlayInput): GooglePlayEvidence {
  const purchaseToken = input.purchaseToken ?? input.purchase_token;
  if (!purchaseToken) {
    throw new Error('google play evidence requires purchase_token');
  }
  const productKind = readProductKind(
    input.productKind ?? input.product_kind ?? 'non_consumable',
  );
  if (!VALID_PRODUCT_KINDS.includes(productKind)) {
    throw new Error('unsupported product kind');
  }
  return {
    purchase_token: purchaseToken,
    product_kind: productKind,
  };
}

export function huaweiEvidence(input: HuaweiInput): HuaweiEvidence {
  const purchaseData = input.purchaseData ?? input.purchase_data;
  const signature = input.signature;
  if (!purchaseData || !signature) {
    throw new Error('huawei evidence requires purchase_data and signature');
  }
  const productKind = readProductKind(
    input.productKind ?? input.product_kind ?? 'non_consumable',
  );
  if (!VALID_PRODUCT_KINDS.includes(productKind)) {
    throw new Error('unsupported product kind');
  }
  return {
    purchase_data: purchaseData,
    signature,
    product_kind: productKind,
  };
}
