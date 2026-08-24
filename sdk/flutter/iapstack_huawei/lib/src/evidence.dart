import 'dart:convert';

import 'package:iapstack/iapstack.dart';
import 'package:iapstack_huawei/src/errors.dart';
import 'package:iapstack_huawei/src/platform.dart';
import 'package:iapstack_huawei/src/product_kind.dart';

/// Exact signed Huawei purchase evidence accepted by IAPStack.
final class HuaweiPurchaseEvidence {
  /// Validates and retains exact signed Huawei JSON bytes represented as a string.
  HuaweiPurchaseEvidence({
    required this.purchaseData,
    required this.signature,
    required this.productKind,
  }) : _decoded = _decodePurchaseData(purchaseData) {
    if (signature.trim().isEmpty) {
      throw const HuaweiIapStackException(
        code: 'invalid_signature',
        message: 'Huawei purchase signature is empty',
      );
    }
  }

  /// Creates evidence from the device bridge without changing signed data.
  factory HuaweiPurchaseEvidence.fromSignedPurchase(
    HuaweiSignedPurchase purchase,
    HuaweiProductKind productKind,
  ) =>
      HuaweiPurchaseEvidence(
        purchaseData: purchase.purchaseData,
        signature: purchase.signature,
        productKind: productKind,
      );

  /// Exact `InAppPurchaseData` JSON string signed by Huawei.
  final String purchaseData;

  /// Detached Huawei signature.
  final String signature;

  /// Product type used for the authoritative server route.
  final HuaweiProductKind productKind;

  final Map<String, Object?> _decoded;

  /// Product ID claimed by the untrusted device payload.
  String get productId => _requiredString(_decoded, 'productId');

  /// Customer binding passed as Huawei `developerPayload`.
  String get developerPayload => _requiredString(_decoded, 'developerPayload');

  /// Encodes the versioned Huawei evidence object without rewriting purchase data.
  Map<String, Object?> toJson() => <String, Object?>{
        'purchase_data': purchaseData,
        'signature': signature,
        'product_kind': productKind.evidenceValue,
      };

  /// Creates a provider-neutral IAPStack submission after local binding checks.
  PurchaseSubmission toSubmission({
    required String externalCustomerId,
    String? expectedProductId,
  }) {
    if (externalCustomerId.trim().isEmpty) {
      throw ArgumentError.value(
          externalCustomerId, 'externalCustomerId', 'must not be empty');
    }
    if (developerPayload != externalCustomerId) {
      throw const HuaweiIapStackException(
        code: 'customer_binding_mismatch',
        message: 'Huawei developer payload does not match the current customer',
      );
    }
    if (expectedProductId != null && productId != expectedProductId) {
      throw const HuaweiIapStackException(
        code: 'product_binding_mismatch',
        message: 'Huawei purchase product does not match the requested product',
      );
    }
    return PurchaseSubmission(
      externalCustomerId: externalCustomerId,
      claimedProducts: <String>[productId],
      evidence: toJson(),
    );
  }
}

Map<String, Object?> _decodePurchaseData(String value) {
  if (value.trim().isEmpty) {
    throw const HuaweiIapStackException(
      code: 'invalid_purchase_data',
      message: 'Huawei purchase data is empty',
    );
  }
  try {
    final decoded = jsonDecode(value);
    if (decoded is! Map<String, Object?>) {
      throw const FormatException('purchase data must be an object');
    }
    return decoded;
  } on FormatException catch (error) {
    throw HuaweiIapStackException(
      code: 'invalid_purchase_data',
      message: 'Huawei purchase data is not a valid JSON object',
      cause: error,
    );
  }
}

String _requiredString(Map<String, Object?> json, String key) {
  final value = json[key];
  if (value is! String || value.isEmpty) {
    throw HuaweiIapStackException(
      code: 'invalid_purchase_data',
      message: 'Huawei purchase data omitted $key',
    );
  }
  return value;
}
