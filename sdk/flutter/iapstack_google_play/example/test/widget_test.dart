import 'package:flutter_test/flutter_test.dart';
import 'package:iapstack_google_play_example/app.dart';
import 'package:iapstack_google_play_example/example_config.dart';

void main() {
  testWidgets('lists missing runtime values without rendering secrets', (
    tester,
  ) async {
    const secret = 'application-secret-that-must-not-be-rendered';
    const config = ExampleConfig(
      baseUrl: '',
      applicationId: 'application-1',
      applicationKey: secret,
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
      applicationKey: 'application-key',
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
}
