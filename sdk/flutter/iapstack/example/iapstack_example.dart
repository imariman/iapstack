// Reads entitlements and the offering document for one signed-in customer.
//
// Run against your own IAPStack server:
//   dart run example/iapstack_example.dart https://iap.example.com my-app customer-123 <customer-token>
//
// The customer token is the short-lived bearer your trusted backend returns
// from POST /v1/applications/{application_id}/customer-sessions. Never ship the
// durable application bearer in an app.
import 'dart:io';

import 'package:iapstack/iapstack.dart';

/// The offering shown when the server cannot be reached on first launch.
final OfferingDocument bundledOffering = OfferingDocument.fromJson(
  <String, Object?>{
    'schema_version': 1,
    'application_id': 'my-app',
    'revision': 'bundled-1',
    'offerings': <Object?>[
      <String, Object?>{
        'id': 'default',
        'packages': <Object?>[
          <String, Object?>{
            'id': 'monthly',
            'store_product_id': 'premium_monthly',
          },
        ],
      },
    ],
  },
);

Future<void> main(List<String> arguments) async {
  if (arguments.length != 4) {
    stderr.writeln(
      'Usage: dart run example/iapstack_example.dart '
      '<base-url> <application-id> <customer-id> <customer-token>',
    );
    exitCode = 64;
    return;
  }
  final client = IapStackClient(
    IapStackConfig(
      baseUri: Uri.parse(arguments[0]),
      applicationId: arguments[1],
      customerToken: arguments[3],
    ),
  );
  try {
    final snapshot = await client.getEntitlements(arguments[2]);
    final hasPremium = snapshot.entitlements
        .any((item) => item.key == 'premium' && item.grantsAccess);
    stdout.writeln('premium: $hasPremium');

    final offering = await client.getOffering(bundledOffering);
    for (final group in offering.offerings) {
      final products = group.packages.map((item) => item.storeProductId);
      stdout.writeln('${group.id}: ${products.join(', ')}');
    }
  } on IapStackApiException catch (error) {
    stderr.writeln('${error.code} (request ${error.requestId}): $error');
    exitCode = 1;
  } finally {
    client.close();
  }
}
