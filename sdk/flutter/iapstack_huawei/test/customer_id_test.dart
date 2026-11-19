import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:iapstack_huawei/iapstack_huawei.dart';

import 'customer_id_vectors.dart';

const String _customerIdMessage = 'must be non-empty valid Unicode without '
    'surrounding whitespace or control characters';

void main() {
  for (final vector in loadCustomerIdVectors()) {
    final outcome = vector.accepted ? 'accepted' : 'rejected';
    test('shared customer ID vector $outcome by evidence: ${vector.name}', () {
      final evidence = HuaweiPurchaseEvidence(
        purchaseData: jsonEncode(<String, Object?>{
          'productId': 'premium_monthly',
          'developerPayload': vector.accepted ? vector.value : 'customer-1',
        }),
        signature: 'signature-premium_monthly',
        productKind: HuaweiProductKind.subscription,
      );
      if (vector.accepted) {
        expect(
          evidence
              .toSubmission(externalCustomerId: vector.value)
              .externalCustomerId,
          vector.value,
        );
        return;
      }
      expect(
        () => evidence.toSubmission(externalCustomerId: vector.value),
        throwsA(isA<ArgumentError>()
            .having((error) => error.name, 'name', 'externalCustomerId')
            .having((error) => error.message, 'message', _customerIdMessage)),
      );
    });
  }
}
