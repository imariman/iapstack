import 'package:iapstack/iapstack.dart';
import 'package:iapstack_apple/src/errors.dart';
import 'package:iapstack_apple/src/product_kind.dart';

/// Exact StoreKit 2 transaction evidence accepted by IAPStack.
final class ApplePurchaseEvidence {
  /// Validates and retains one opaque signed transaction only in memory.
  ApplePurchaseEvidence({
    required this.signedTransaction,
    required this.productKind,
  }) {
    if (signedTransaction.trim().isEmpty ||
        '.'.allMatches(signedTransaction).length != 2) {
      throw const AppleIapStackException(
        code: 'invalid_signed_transaction',
        message: 'The StoreKit signed transaction is not a compact JWS',
      );
    }
  }

  /// Compact StoreKit 2 JWS returned as server verification data.
  final String signedTransaction;

  /// Product route used for authoritative App Store Server API verification.
  final AppleProductKind productKind;

  /// Encodes the versioned Apple evidence object.
  Map<String, Object?> toJson() => <String, Object?>{
    'signed_transaction': signedTransaction,
    'product_kind': productKind.evidenceValue,
  };

  /// Creates a provider-neutral submission without exposing the JWS elsewhere.
  PurchaseSubmission toSubmission({
    required String externalCustomerId,
    required String productId,
  }) {
    if (externalCustomerId.trim().isEmpty || productId.trim().isEmpty) {
      throw ArgumentError('externalCustomerId and productId must not be empty');
    }
    return PurchaseSubmission(
      externalCustomerId: externalCustomerId,
      claimedProducts: <String>[productId],
      evidence: toJson(),
    );
  }

  @override
  String toString() =>
      'ApplePurchaseEvidence(productKind: ${productKind.name}, signedTransaction: <redacted>)';
}
