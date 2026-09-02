import 'package:iapstack_google_play/src/product_kind.dart';

/// Recurrence behavior reported for one Google Play subscription pricing phase.
enum GooglePlayPricingRecurrence {
  /// The phase repeats for [GooglePlayPricingPhase.billingCycleCount] cycles.
  finite,

  /// The phase repeats until the subscription is cancelled.
  infinite,

  /// The phase is charged once.
  nonRecurring,
}

/// One exact subscription pricing phase returned by Google Play Billing.
final class GooglePlayPricingPhase {
  /// Creates one immutable localized pricing phase.
  const GooglePlayPricingPhase({
    required this.billingCycleCount,
    required this.billingPeriod,
    required this.formattedPrice,
    required this.priceMicros,
    required this.currencyCode,
    required this.recurrence,
  });

  /// Number of cycles for a finite phase, or zero for non-finite phases.
  final int billingCycleCount;

  /// ISO 8601 billing period, such as `P1M`.
  final String billingPeriod;

  /// Localized price including its currency symbol.
  final String formattedPrice;

  /// Exact price in millionths of [currencyCode].
  final int priceMicros;

  /// ISO 4217 currency code.
  final String currencyCode;

  /// Provider recurrence behavior for this phase.
  final GooglePlayPricingRecurrence recurrence;

  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      other is GooglePlayPricingPhase &&
          billingCycleCount == other.billingCycleCount &&
          billingPeriod == other.billingPeriod &&
          formattedPrice == other.formattedPrice &&
          priceMicros == other.priceMicros &&
          currencyCode == other.currencyCode &&
          recurrence == other.recurrence;

  @override
  int get hashCode => Object.hash(
    billingCycleCount,
    billingPeriod,
    formattedPrice,
    priceMicros,
    currencyCode,
    recurrence,
  );
}

/// Display-safe Google Play product, base plan, or subscription offer.
final class GooglePlayProduct {
  /// Creates immutable product details returned by Play Billing.
  GooglePlayProduct({
    required this.id,
    required this.kind,
    required this.title,
    required this.description,
    required this.price,
    required this.priceMicros,
    required this.currencyCode,
    this.offerToken,
    this.basePlanId,
    this.offerId,
    List<String> offerTags = const <String>[],
    List<GooglePlayPricingPhase> pricingPhases =
        const <GooglePlayPricingPhase>[],
  }) : offerTags = List<String>.unmodifiable(offerTags),
       pricingPhases = List<GooglePlayPricingPhase>.unmodifiable(pricingPhases);

  /// Provider product identifier configured in Play Console and IAPStack.
  final String id;

  /// Product kind reported by Google Play Billing.
  final GooglePlayProductKind kind;

  /// Localized provider title.
  final String title;

  /// Localized provider description.
  final String description;

  /// Localized current or first-phase price.
  final String price;

  /// Exact current or first-phase price in millionths of [currencyCode].
  final int priceMicros;

  /// Price in major currency units, retained for display compatibility.
  double get rawPrice => priceMicros / 1000000;

  /// ISO 4217 provider currency code.
  final String currencyCode;

  /// Opaque token required to purchase this subscription offer.
  final String? offerToken;

  /// Subscription base-plan identifier, or null for one-time products.
  final String? basePlanId;

  /// Discounted subscription offer identifier, when present.
  final String? offerId;

  /// Safe Play Console tags associated with this subscription offer.
  final List<String> offerTags;

  /// Complete ordered pricing schedule for this subscription offer.
  final List<GooglePlayPricingPhase> pricingPhases;

  /// Stable in-memory key used to select one queried product offer.
  String get selectionKey => '$id\u0000${offerToken ?? ''}';

  /// Whether the returned row contains everything needed to open checkout.
  bool get isPurchasable {
    if (id.isEmpty ||
        price.isEmpty ||
        priceMicros < 0 ||
        !RegExp(r'^[A-Z]{3}$').hasMatch(currencyCode)) {
      return false;
    }
    return switch (kind) {
      GooglePlayProductKind.nonConsumable =>
        offerToken == null && basePlanId == null && pricingPhases.isEmpty,
      GooglePlayProductKind.subscription =>
        offerToken?.isNotEmpty == true &&
            basePlanId?.isNotEmpty == true &&
            pricingPhases.isNotEmpty,
    };
  }

  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      other is GooglePlayProduct &&
          id == other.id &&
          kind == other.kind &&
          title == other.title &&
          description == other.description &&
          price == other.price &&
          priceMicros == other.priceMicros &&
          currencyCode == other.currencyCode &&
          offerToken == other.offerToken &&
          basePlanId == other.basePlanId &&
          offerId == other.offerId &&
          _listsEqual(offerTags, other.offerTags) &&
          _listsEqual(pricingPhases, other.pricingPhases);

  @override
  int get hashCode => Object.hash(
    id,
    kind,
    title,
    description,
    price,
    priceMicros,
    currencyCode,
    offerToken,
    basePlanId,
    offerId,
    Object.hashAll(offerTags),
    Object.hashAll(pricingPhases),
  );
}

/// One bounded Google Play product query result.
final class GooglePlayProductQuery {
  /// Creates one immutable query result.
  GooglePlayProductQuery({
    required List<GooglePlayProduct> products,
    required Set<String> notFoundProductIds,
  }) : products = List<GooglePlayProduct>.unmodifiable(products),
       notFoundProductIds = Set<String>.unmodifiable(notFoundProductIds);

  /// Product and offer rows returned by Google Play.
  final List<GooglePlayProduct> products;

  /// Requested product identifiers that Google Play could not resolve.
  final Set<String> notFoundProductIds;
}

bool _listsEqual<T>(List<T> left, List<T> right) {
  if (identical(left, right)) {
    return true;
  }
  if (left.length != right.length) {
    return false;
  }
  for (var index = 0; index < left.length; index++) {
    if (left[index] != right[index]) {
      return false;
    }
  }
  return true;
}
