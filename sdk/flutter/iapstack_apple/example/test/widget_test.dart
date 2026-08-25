// This is a basic Flutter widget test.
//
// To perform an interaction with a widget in your test, use the WidgetTester
// utility in the flutter_test package. For example, you can send tap and scroll
// gestures. You can also use WidgetTester to find child widgets in the widget
// tree, read text, and verify that the values of widget properties are correct.

import 'package:flutter_test/flutter_test.dart';
import 'package:iapstack_apple_example/app.dart';
import 'package:iapstack_apple_example/example_config.dart';

void main() {
  testWidgets('shows safe missing configuration without rendering a bearer', (
    WidgetTester tester,
  ) async {
    await tester.pumpWidget(
      const ExampleApp(
        config: ExampleConfig(
          baseUrl: '',
          applicationId: '',
          applicationKey: 'must-not-render',
          externalCustomerId: '',
          subscriptionProductId: 'premium_monthly',
          nonConsumableProductId: 'premium_lifetime',
        ),
      ),
    );

    expect(find.text('Configuration required'), findsOneWidget);
    expect(find.text('IAPSTACK_BASE_URL'), findsOneWidget);
    expect(find.text('must-not-render'), findsNothing);
  });
}
