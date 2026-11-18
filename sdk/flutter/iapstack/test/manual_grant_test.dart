import 'package:iapstack/iapstack.dart';
import 'package:test/test.dart';

void main() {
  test(
      'Entitlement keeps the manual_grant reason and grants access until the end',
      () {
    final entitlement = Entitlement.fromJson(<String, Object?>{
      'key': 'premium',
      'access': 'allowed',
      'reason': 'manual_grant',
      'effective_starts_at': '2026-11-18T10:00:00Z',
      'effective_ends_at': '2026-12-18T10:00:00Z',
      'version': 2,
    });

    expect(entitlement.reason, 'manual_grant');
    expect(entitlement.grantsAccessAt(DateTime.utc(2026, 12, 1)), isTrue);
    expect(entitlement.grantsAccessAt(DateTime.utc(2026, 12, 18, 10)), isFalse);
  });
}
