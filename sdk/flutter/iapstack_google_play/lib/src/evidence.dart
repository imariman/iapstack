import 'package:iapstack/iapstack.dart';
import 'package:iapstack_google_play/src/errors.dart';
import 'package:iapstack_google_play/src/product_kind.dart';

/// Exact Google Play purchase-token evidence accepted by IAPStack.
final class GooglePlayPurchaseEvidence {
  /// Validates and retains one opaque purchase token only in memory.
  GooglePlayPurchaseEvidence({
    required this.purchaseToken,
    required this.productKind,
  }) {
    if (purchaseToken.trim().isEmpty) {
      throw const GooglePlayIapStackException(
        code: 'invalid_purchase_token',
        message: 'Google Play purchase token is empty',
      );
    }
  }

  /// Opaque token returned by Play Billing.
  final String purchaseToken;

  /// Product route used for authoritative Android Publisher verification.
  final GooglePlayProductKind productKind;

  /// Encodes the versioned Google Play evidence object.
  Map<String, Object?> toJson() => <String, Object?>{
    'purchase_token': purchaseToken,
    'product_kind': productKind.evidenceValue,
  };

  /// Creates a provider-neutral submission without exposing the token elsewhere.
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
      'GooglePlayPurchaseEvidence(productKind: ${productKind.name}, purchaseToken: <redacted>)';
}
