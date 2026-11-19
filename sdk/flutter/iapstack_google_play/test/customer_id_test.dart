import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:iapstack/iapstack.dart';
import 'package:iapstack_google_play/iapstack_google_play.dart';

import 'customer_id_vectors.dart';

const String _customerIdMessage =
    'must be non-empty valid Unicode without '
    'surrounding whitespace or control characters';

void main() {
  for (final vector in loadCustomerIdVectors()) {
    final outcome = vector.accepted ? 'accepted' : 'rejected';
    test('shared customer ID vector $outcome before Billing opens: '
        '${vector.name}', () async {
      final platform = _RecordingPlatform();
      final googlePlay = _googlePlay(platform);
      await googlePlay.queryProducts(const <String>{'premium_lifetime'});

      if (vector.accepted) {
        await googlePlay.launchPurchase(
          externalCustomerId: vector.value,
          product: _lifetime,
        );
        expect(platform.launchedAccountId, vector.value);
        return;
      }
      final rejection = isA<ArgumentError>()
          .having((error) => error.name, 'name', 'externalCustomerId')
          .having((error) => error.message, 'message', _customerIdMessage);
      await expectLater(
        googlePlay.launchPurchase(
          externalCustomerId: vector.value,
          product: _lifetime,
        ),
        throwsA(rejection),
      );
      await expectLater(
        googlePlay.restorePurchases(externalCustomerId: vector.value),
        throwsA(rejection),
      );
      expect(
        () =>
            GooglePlayPurchaseEvidence(
              purchaseToken: 'token',
              productKind: GooglePlayProductKind.nonConsumable,
            ).toSubmission(
              externalCustomerId: vector.value,
              productId: 'premium_lifetime',
            ),
        throwsA(rejection),
      );
      expect(platform.launchedAccountId, isNull);
      expect(platform.ownedPurchaseCalls, 0);
    });
  }

  test(
    'keeps Play Billing obfuscatedAccountId limit of 64 characters',
    () async {
      final platform = _RecordingPlatform();
      final googlePlay = _googlePlay(platform);
      await googlePlay.queryProducts(const <String>{'premium_lifetime'});

      await expectLater(
        googlePlay.launchPurchase(
          externalCustomerId: 'a' * 65,
          product: _lifetime,
        ),
        throwsA(
          isA<ArgumentError>().having(
            (error) => error.message,
            'message',
            'must be at most 64 characters, the Google Play '
                'obfuscatedAccountId limit',
          ),
        ),
      );
      expect(platform.launchedAccountId, isNull);
      await googlePlay.launchPurchase(
        externalCustomerId: 'a' * 64,
        product: _lifetime,
      );
      expect(platform.launchedAccountId, 'a' * 64);
    },
  );
}

final GooglePlayProduct _lifetime = GooglePlayProduct(
  id: 'premium_lifetime',
  kind: GooglePlayProductKind.nonConsumable,
  title: 'Premium Lifetime',
  description: 'Premium access',
  price: r'$49.99',
  priceMicros: 49990000,
  currencyCode: 'USD',
);

GooglePlayIapStack _googlePlay(GooglePlayIapPlatform platform) =>
    GooglePlayIapStack(
      client: IapStackClient(
        IapStackConfig(
          baseUri: Uri.parse('https://iap.example'),
          applicationId: 'application-1',
          customerToken: 'customer-token',
          retryPolicy: const IapStackRetryPolicy(maxAttempts: 1),
        ),
        httpClient: MockClient((request) async => http.Response('{}', 500)),
      ),
      productKinds: const <String, GooglePlayProductKind>{
        'premium_lifetime': GooglePlayProductKind.nonConsumable,
      },
      platform: platform,
    );

final class _RecordingPlatform implements GooglePlayIapPlatform {
  String? launchedAccountId;
  int ownedPurchaseCalls = 0;

  @override
  Stream<GooglePlayPurchase> get purchaseUpdates => const Stream.empty();

  @override
  Future<bool> isAvailable() async => true;

  @override
  Future<GooglePlayProductQuery> queryProducts(Set<String> productIds) async =>
      GooglePlayProductQuery(
        products: <GooglePlayProduct>[_lifetime],
        notFoundProductIds: const <String>{},
      );

  @override
  Future<void> launchPurchase({
    required GooglePlayProduct product,
    required String obfuscatedAccountId,
  }) async {
    launchedAccountId = obfuscatedAccountId;
  }

  @override
  Future<List<GooglePlayPurchase>> ownedPurchases({
    required String obfuscatedAccountId,
  }) async {
    ownedPurchaseCalls++;
    return const <GooglePlayPurchase>[];
  }
}
