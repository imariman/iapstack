import 'package:flutter_test/flutter_test.dart';
import 'package:iapstack/iapstack.dart';
import 'package:iapstack_apple_example/example_cubit.dart';

void main() {
  const original = Entitlement(
    key: 'premium',
    access: 'allowed',
    reason: 'purchased',
    version: 1,
  );

  test('accepts identical logical projections regardless of ordering', () {
    const second = Entitlement(
      key: 'bonus',
      access: 'denied',
      reason: 'expired',
      version: 3,
    );

    expect(
      sameLogicalEntitlementProjections(
        const <Entitlement>[original, second],
        const <Entitlement>[second, original],
      ),
      isTrue,
    );
  });

  test('rejects a projection version advanced by duplicate restore', () {
    const advanced = Entitlement(
      key: 'premium',
      access: 'allowed',
      reason: 'purchased',
      version: 2,
    );

    expect(
      sameLogicalEntitlementProjections(
        const <Entitlement>[original],
        const <Entitlement>[advanced],
      ),
      isFalse,
    );
  });
}
