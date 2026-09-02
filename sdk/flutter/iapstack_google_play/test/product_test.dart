import 'package:flutter_test/flutter_test.dart';
import 'package:iapstack_google_play/iapstack_google_play.dart';

void main() {
  group('GooglePlayProduct', () {
    test('requires complete kind-specific checkout metadata', () {
      final cases = <({String name, GooglePlayProduct product, bool expected})>[
        (name: 'one-time product', product: _oneTimeProduct(), expected: true),
        (
          name: 'subscription offer',
          product: _subscriptionProduct(),
          expected: true,
        ),
        (
          name: 'empty identifier',
          product: _oneTimeProduct(id: ''),
          expected: false,
        ),
        (
          name: 'negative price',
          product: _oneTimeProduct(priceMicros: -1),
          expected: false,
        ),
        (
          name: 'invalid currency',
          product: _oneTimeProduct(currencyCode: 'usd'),
          expected: false,
        ),
        (
          name: 'one-time product with offer token',
          product: _oneTimeProduct(offerToken: 'unexpected'),
          expected: false,
        ),
        (
          name: 'subscription without offer token',
          product: _subscriptionProduct(offerToken: ''),
          expected: false,
        ),
        (
          name: 'subscription without base plan',
          product: _subscriptionProduct(basePlanId: ''),
          expected: false,
        ),
        (
          name: 'subscription without pricing phases',
          product: _subscriptionProduct(
            pricingPhases: const <GooglePlayPricingPhase>[],
          ),
          expected: false,
        ),
      ];

      for (final testCase in cases) {
        expect(
          testCase.product.isPurchasable,
          testCase.expected,
          reason: testCase.name,
        );
      }
    });

    test('uses value equality and protects nested collections', () {
      final tags = <String>['intro'];
      final phases = <GooglePlayPricingPhase>[_phase];
      final product = _subscriptionProduct(
        offerTags: tags,
        pricingPhases: phases,
      );
      final equal = _subscriptionProduct(
        offerTags: const <String>['intro'],
        pricingPhases: const <GooglePlayPricingPhase>[_phase],
      );

      tags.add('mutated');
      phases.clear();

      expect(product, equal);
      expect(product.hashCode, equal.hashCode);
      expect(product.offerTags, const <String>['intro']);
      expect(product.pricingPhases, const <GooglePlayPricingPhase>[_phase]);
      expect(() => product.offerTags.add('blocked'), throwsUnsupportedError);
      expect(() => product.pricingPhases.clear(), throwsUnsupportedError);
      expect(product.selectionKey, 'premium_monthly\u0000offer-token');
    });

    test('query snapshots input collections', () {
      final products = <GooglePlayProduct>[_oneTimeProduct()];
      final missing = <String>{'missing'};
      final query = GooglePlayProductQuery(
        products: products,
        notFoundProductIds: missing,
      );

      products.clear();
      missing.clear();

      expect(query.products, hasLength(1));
      expect(query.notFoundProductIds, const <String>{'missing'});
      expect(() => query.products.clear(), throwsUnsupportedError);
      expect(() => query.notFoundProductIds.clear(), throwsUnsupportedError);
    });
  });

  test('purchase verification status is fail closed', () {
    for (final status in GooglePlayPurchaseStatus.values) {
      final purchase = GooglePlayPurchase(
        purchaseToken: 'token',
        productIds: const <String>['premium'],
        status: status,
        isAcknowledged: false,
      );
      expect(
        purchase.canVerify,
        status == GooglePlayPurchaseStatus.pending ||
            status == GooglePlayPurchaseStatus.purchased ||
            status == GooglePlayPurchaseStatus.restored,
        reason: status.name,
      );
    }
  });
}

const GooglePlayPricingPhase _phase = GooglePlayPricingPhase(
  billingCycleCount: 0,
  billingPeriod: 'P1M',
  formattedPrice: r'$4.99',
  priceMicros: 4990000,
  currencyCode: 'USD',
  recurrence: GooglePlayPricingRecurrence.infinite,
);

GooglePlayProduct _oneTimeProduct({
  String id = 'premium_lifetime',
  int priceMicros = 49990000,
  String currencyCode = 'USD',
  String? offerToken,
}) => GooglePlayProduct(
  id: id,
  kind: GooglePlayProductKind.nonConsumable,
  title: 'Premium Lifetime',
  description: 'Permanent access',
  price: r'$49.99',
  priceMicros: priceMicros,
  currencyCode: currencyCode,
  offerToken: offerToken,
);

GooglePlayProduct _subscriptionProduct({
  String offerToken = 'offer-token',
  String basePlanId = 'monthly',
  List<String> offerTags = const <String>[],
  List<GooglePlayPricingPhase> pricingPhases = const <GooglePlayPricingPhase>[
    _phase,
  ],
}) => GooglePlayProduct(
  id: 'premium_monthly',
  kind: GooglePlayProductKind.subscription,
  title: 'Premium Monthly',
  description: 'Monthly access',
  price: r'$4.99',
  priceMicros: 4990000,
  currencyCode: 'USD',
  offerToken: offerToken,
  basePlanId: basePlanId,
  offerTags: offerTags,
  pricingPhases: pricingPhases,
);
