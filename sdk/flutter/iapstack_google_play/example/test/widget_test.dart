import 'package:flutter_test/flutter_test.dart';
import 'package:flutter/material.dart';
import 'package:iapstack_google_play/iapstack_google_play.dart';
import 'package:iapstack_google_play_example/app.dart';
import 'package:iapstack_google_play_example/example_config.dart';
import 'package:iapstack_google_play_example/product_offer_card.dart';

void main() {
  testWidgets('lists missing runtime values without rendering secrets', (
    tester,
  ) async {
    const secret = 'application-secret-that-must-not-be-rendered';
    const config = ExampleConfig(
      baseUrl: '',
      applicationId: 'application-1',
      customerToken: secret,
      externalCustomerId: 'opaque-customer-1',
      subscriptionProductId: '',
      nonConsumableProductId: '',
    );

    await tester.pumpWidget(const ExampleApp(config: config));

    expect(find.text('Runtime configuration required'), findsOneWidget);
    expect(find.text('IAPSTACK_BASE_URL'), findsOneWidget);
    expect(
      find.textContaining('IAPSTACK_GOOGLE_PLAY_SUBSCRIPTION_ID'),
      findsOneWidget,
    );
    expect(find.textContaining(secret), findsNothing);
  });

  test('builds an explicit product-kind catalog', () {
    const config = ExampleConfig(
      baseUrl: 'https://iap.example',
      applicationId: 'application-1',
      customerToken: 'customer-token',
      externalCustomerId: 'opaque-customer-1',
      subscriptionProductId: 'premium_monthly',
      nonConsumableProductId: 'premium_lifetime',
    );

    expect(config.isComplete, isTrue);
    expect(
      config.productKinds.keys,
      containsAll(<String>['premium_monthly', 'premium_lifetime']),
    );
  });

  testWidgets('renders base plan and exact pricing schedule', (tester) async {
    final product = GooglePlayProduct(
      id: 'premium_monthly',
      kind: GooglePlayProductKind.subscription,
      title: 'Premium Monthly',
      description: 'Premium access',
      price: r'$0.00',
      priceMicros: 0,
      currencyCode: 'USD',
      offerToken: 'trial-token',
      basePlanId: 'monthly',
      offerId: 'trial',
      offerTags: const <String>['new-users'],
      pricingPhases: const <GooglePlayPricingPhase>[
        GooglePlayPricingPhase(
          billingCycleCount: 1,
          billingPeriod: 'P7D',
          formattedPrice: r'$0.00',
          priceMicros: 0,
          currencyCode: 'USD',
          recurrence: GooglePlayPricingRecurrence.finite,
        ),
      ],
    );

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: ProductOfferCard(
            product: product,
            enabled: true,
            onPurchase: () {},
          ),
        ),
      ),
    );

    expect(find.text('Base plan: monthly'), findsOneWidget);
    expect(find.text('Offer: trial'), findsOneWidget);
    expect(find.text('Tag: new-users'), findsOneWidget);
    expect(find.text(r'$0.00 / P7D · finite'), findsOneWidget);
  });
}
