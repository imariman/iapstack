import 'package:flutter_test/flutter_test.dart';
import 'package:iapstack_apple_example/example_config.dart';

void main() {
  test('accepts the customer token as a runtime value', () {
    final config = ExampleConfig.fromEnvironment(
      customerToken: 'runtime-customer-token',
    );

    expect(config.customerToken, 'runtime-customer-token');
  });
}
