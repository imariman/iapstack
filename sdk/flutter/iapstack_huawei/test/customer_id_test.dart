import 'package:flutter_test/flutter_test.dart';
import 'package:iapstack_huawei/src/customer_id.dart';

import 'customer_id_vectors.dart';

void main() {
  for (final vector in loadCustomerIdVectors()) {
    final outcome = vector.accepted ? 'accepted' : 'rejected';
    test('shared customer ID vector $outcome: ${vector.name}', () {
      if (vector.accepted) {
        validateExternalCustomerId(vector.value);
        return;
      }
      expect(
        () => validateExternalCustomerId(vector.value),
        throwsA(isA<ArgumentError>()
            .having((error) => error.name, 'name', 'externalCustomerId')),
      );
    });
  }
}
