import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:iapstack/iapstack.dart';
import 'package:test/test.dart';

import 'customer_id_vectors.dart';

const String _customerIdMessage = 'must be non-empty valid Unicode without '
    'surrounding whitespace or control characters';

void main() {
  for (final vector in loadCustomerIdVectors()) {
    final outcome = vector.accepted ? 'accepted' : 'rejected';
    test(
        'shared customer ID vector $outcome before any request: '
        '${vector.name}', () async {
      var calls = 0;
      final client = IapStackClient(
        IapStackConfig(
          baseUri: Uri.parse('https://iap.example/proxy'),
          applicationId: 'application-1',
          customerToken: 'customer-token',
          retryPolicy: const IapStackRetryPolicy(maxAttempts: 1),
        ),
        httpClient: MockClient((request) async {
          calls++;
          return http.Response(
              jsonEncode(<String, Object?>{
                'verified_at': '2026-08-24T20:00:00Z',
                'customer_id': 'customer-internal',
                'entitlements': const <Object?>[],
              }),
              200);
        }),
      );
      final purchase = PurchaseSubmission(
        externalCustomerId: vector.value,
        claimedProducts: const <String>['premium_lifetime'],
        evidence: const <String, Object?>{'signature': 'signed'},
      );

      if (vector.accepted) {
        await client.getEntitlements(vector.value);
        await client.verifyPurchase(purchase);
        expect(calls, 2);
        return;
      }
      final rejection = throwsA(isA<ArgumentError>()
          .having((error) => error.name, 'name', 'externalCustomerId')
          .having((error) => error.message, 'message', _customerIdMessage));
      await expectLater(client.getEntitlements(vector.value), rejection);
      await expectLater(client.verifyPurchase(purchase), rejection);
      expect(calls, 0);
    });
  }
}
