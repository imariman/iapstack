import 'package:flutter_test/flutter_test.dart';
import 'package:iapstack_apple/iapstack_apple.dart';

import 'customer_id_vectors.dart';

const String _customerIdMessage =
    'must be non-empty valid Unicode without '
    'surrounding whitespace or control characters';

void main() {
  for (final vector in loadCustomerIdVectors()) {
    final outcome = vector.accepted ? 'accepted' : 'rejected';
    test('shared customer ID vector $outcome by evidence: ${vector.name}', () {
      final evidence = ApplePurchaseEvidence(
        signedTransaction: 'header.payload.signature',
        productKind: AppleProductKind.subscription,
      );
      submit() => evidence.toSubmission(
        externalCustomerId: vector.value,
        productId: 'premium_monthly',
      );
      if (vector.accepted) {
        expect(submit().externalCustomerId, vector.value);
        return;
      }
      expect(
        submit,
        throwsA(
          isA<ArgumentError>()
              .having((error) => error.name, 'name', 'externalCustomerId')
              .having((error) => error.message, 'message', _customerIdMessage),
        ),
      );
    });
  }
}
