import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iapstack_huawei_example/configuration_page.dart';

void main() {
  testWidgets('shows missing key names without secret values', (tester) async {
    await tester.pumpWidget(
      const MaterialApp(
        home: ConfigurationPage(
          missingValues: <String>['IAPSTACK_APPLICATION_KEY'],
        ),
      ),
    );

    expect(find.text('Runtime configuration required'), findsOneWidget);
    expect(find.text('IAPSTACK_APPLICATION_KEY'), findsOneWidget);
    expect(find.textContaining('Bearer'), findsNothing);
  });
}
