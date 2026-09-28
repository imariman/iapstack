import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:iapstack/iapstack.dart';
import 'package:test/test.dart';

void main() {
  group('IapStackClient', () {
    test('sends the exact verify contract and decodes projections', () async {
      late http.Request captured;
      final client = IapStackClient(
        _config(),
        httpClient: MockClient((request) async {
          captured = request;
          return http.Response(jsonEncode(_verificationJson), 200);
        }),
      );

      final result = await client.verifyPurchase(_purchase(),
          requestId: 'request-client-1');

      expect(captured.method, 'POST');
      expect(captured.url.path,
          '/proxy/v1/applications/application-1/purchases:verify');
      expect(captured.headers['Authorization'], 'Bearer customer-token');
      expect(captured.headers['Content-Type'], 'application/json');
      expect(captured.headers['X-Request-ID'], 'request-client-1');
      expect(captured.headers['X-IAPStack-SDK'], startsWith('flutter/'));
      expect(jsonDecode(captured.body), <String, Object?>{
        'external_customer_id': 'customer-external',
        'claimed_products': <String>['premium_lifetime'],
        'evidence': <String, Object?>{
          'purchase_data': '{"productId":"premium_lifetime"}',
          'signature': 'signed',
          'product_kind': 'non_consumable',
        },
      });
      expect(result.customerId, 'customer-internal');
      expect(result.verifiedAt, DateTime.utc(2026, 8, 24, 20));
      expect(result.entitlements.single.grantsAccess, isTrue);
      expect(result.entitlements.single.effectiveEndsAt, isNull);
    });

    test('sends restore batches and preserves result order', () async {
      late Object? body;
      final client = IapStackClient(
        _config(),
        httpClient: MockClient((request) async {
          body = jsonDecode(request.body);
          return http.Response(
            jsonEncode(<String, Object?>{
              'results': <Object?>[_verificationJson, _verificationJson]
            }),
            200,
          );
        }),
      );

      final result = await client.restorePurchases(
          <PurchaseSubmission>[_purchase(), _purchase('pro_monthly')]);

      final json = body! as Map<String, dynamic>;
      expect((json['purchases']! as List<dynamic>), hasLength(2));
      expect(result.results, hasLength(2));
    });

    test('rejects external customer IDs with surrounding whitespace', () async {
      var calls = 0;
      final client = IapStackClient(
        _config(),
        httpClient: MockClient((request) async {
          calls++;
          return http.Response('{}', 200);
        }),
      );

      expect(() => client.getEntitlements(' customer-external'),
          throwsArgumentError);
      expect(
        () => client.verifyPurchase(PurchaseSubmission(
          externalCustomerId: 'customer-external ',
          claimedProducts: const <String>['premium_lifetime'],
          evidence: const <String, Object?>{'signature': 'signed'},
        )),
        throwsArgumentError,
      );
      expect(calls, 0);
    });

    test('treats invalid UTF-8 as a non-retried protocol error', () async {
      var attempts = 0;
      final client = IapStackClient(
        _config(
          retryPolicy: const IapStackRetryPolicy(
            maxAttempts: 3,
            baseDelay: Duration.zero,
            maxDelay: Duration.zero,
          ),
        ),
        httpClient: MockClient((request) async {
          attempts++;
          return http.Response.bytes(<int>[0x7b, 0xff, 0xfe, 0x7d], 200);
        }),
      );

      await expectLater(
        client.getEntitlements('customer-external'),
        throwsA(isA<IapStackProtocolException>()),
      );
      expect(attempts, 1);
    });

    test('retries a transient status whose body is not valid UTF-8', () async {
      var attempts = 0;
      final client = IapStackClient(
        _config(
          retryPolicy: const IapStackRetryPolicy(
            maxAttempts: 2,
            baseDelay: Duration.zero,
            maxDelay: Duration.zero,
          ),
        ),
        httpClient: MockClient((request) async {
          attempts++;
          if (attempts == 1) {
            return http.Response.bytes(<int>[0xff, 0xfe], 503);
          }
          return http.Response(
            jsonEncode(<String, Object?>{
              'customer_id': 'customer-internal',
              'entitlements': _verificationJson['entitlements'],
            }),
            200,
          );
        }),
      );

      final snapshot = await client.getEntitlements('customer-external');

      expect(attempts, 2);
      expect(snapshot.customerId, 'customer-internal');
    });

    test('keeps the HTTP status when an error body is not valid UTF-8',
        () async {
      for (final (status, retryable, expectedAttempts) in <(int, bool, int)>[
        (429, true, 2),
        (403, false, 1),
      ]) {
        var attempts = 0;
        final client = IapStackClient(
          _config(
            retryPolicy: const IapStackRetryPolicy(
              maxAttempts: 2,
              baseDelay: Duration.zero,
              maxDelay: Duration.zero,
            ),
          ),
          httpClient: MockClient((request) async {
            attempts++;
            return http.Response.bytes(<int>[0xff, 0xfe], status);
          }),
        );

        await expectLater(
          client.getEntitlements('customer-external'),
          throwsA(
            isA<IapStackApiException>()
                .having((error) => error.statusCode, 'statusCode', status)
                .having((error) => error.code, 'code', 'http_error')
                .having((error) => error.retryable, 'retryable', retryable),
          ),
        );
        expect(attempts, expectedAttempts, reason: 'HTTP $status');
      }
    });

    test('exposes the Retry-After cooldown on the API exception', () async {
      final client = IapStackClient(
        _config(),
        httpClient: MockClient((request) async {
          return http.Response(
            '{"error":{"code":"rate_limited","message":"slow down"}}',
            429,
            headers: <String, String>{'retry-after': '17'},
          );
        }),
      );

      await expectLater(
        client.getEntitlements('customer-external'),
        throwsA(
          isA<IapStackApiException>()
              .having((error) => error.retryable, 'retryable', isTrue)
              .having((error) => error.retryAfter, 'retryAfter',
                  const Duration(seconds: 17)),
        ),
      );
    });

    test('waits at least the Retry-After cooldown before retrying', () async {
      var attempts = 0;
      final client = IapStackClient(
        _config(
          retryPolicy: const IapStackRetryPolicy(
            maxAttempts: 2,
            baseDelay: Duration.zero,
            maxDelay: Duration.zero,
          ),
        ),
        httpClient: MockClient((request) async {
          attempts++;
          if (attempts == 1) {
            return http.Response(
              '{"error":{"code":"provider_unavailable","message":"x"}}',
              503,
              headers: <String, String>{'retry-after': '1'},
            );
          }
          return http.Response(
            jsonEncode(<String, Object?>{
              'customer_id': 'customer-internal',
              'entitlements': _verificationJson['entitlements'],
            }),
            200,
          );
        }),
      );

      final stopwatch = Stopwatch()..start();
      await client.getEntitlements('customer-external');

      expect(attempts, 2);
      expect(
          stopwatch.elapsed, greaterThanOrEqualTo(const Duration(seconds: 1)),
          reason: 'the client must sleep for the Retry-After second');
    });

    test('an elapsed Retry-After does not block the retry', () async {
      var attempts = 0;
      final client = IapStackClient(
        _config(
          retryPolicy: const IapStackRetryPolicy(
            maxAttempts: 2,
            baseDelay: Duration.zero,
            maxDelay: Duration.zero,
          ),
        ),
        httpClient: MockClient((request) async {
          attempts++;
          if (attempts == 1) {
            return http.Response(
              '{"error":{"code":"provider_unavailable","message":"x"}}',
              503,
              headers: <String, String>{'retry-after': '0'},
            );
          }
          return http.Response(
            jsonEncode(<String, Object?>{
              'customer_id': 'customer-internal',
              'entitlements': _verificationJson['entitlements'],
            }),
            200,
          );
        }),
      );

      final snapshot = await client.getEntitlements('customer-external');

      expect(attempts, 2);
      expect(snapshot.customerId, 'customer-internal');
    });

    test('rejects entitlement versions below one', () async {
      final client = IapStackClient(
        _config(),
        httpClient: MockClient((request) async => http.Response(
              jsonEncode(<String, Object?>{
                'customer_id': 'customer-internal',
                'entitlements': <Object?>[
                  <String, Object?>{
                    'key': 'premium',
                    'access': 'allowed',
                    'reason': 'purchase_valid',
                    'version': 0,
                  },
                ],
              }),
              200,
            )),
      );

      await expectLater(
        client.getEntitlements('customer-external'),
        throwsA(isA<IapStackProtocolException>()),
      );
    });

    test('restores any number of purchases in ordered batches of 100',
        () async {
      final batches = <int>[];
      final requestIds = <String?>[];
      final client = IapStackClient(
        _config(),
        httpClient: MockClient((request) async {
          final purchases = (jsonDecode(request.body)
              as Map<String, dynamic>)['purchases'] as List<dynamic>;
          batches.add(purchases.length);
          requestIds.add(request.headers['X-Request-ID']);
          return http.Response(
            jsonEncode(<String, Object?>{
              'results':
                  List<Object?>.filled(purchases.length, _verificationJson),
            }),
            200,
          );
        }),
      );

      final result = await client.restorePurchasesInBatches(
        List<PurchaseSubmission>.generate(
            250, (index) => _purchase('product_$index')),
        requestId: ' restore-1 ',
      );

      expect(batches, <int>[100, 100, 50]);
      expect(requestIds, <String?>['restore-1', 'restore-1-2', 'restore-1-3']);
      expect(result.results, hasLength(250));
      expect(
        (await client.restorePurchasesInBatches(const <PurchaseSubmission>[]))
            .results,
        isEmpty,
      );
    });

    test('fails closed when an allowed entitlement reaches its effective end',
        () {
      final endsAt = DateTime.utc(2026, 8, 26, 12);
      final entitlement = Entitlement(
        key: 'premium',
        access: 'allowed',
        reason: 'canceled_at_period_end',
        version: 7,
        effectiveStartsAt: endsAt.subtract(const Duration(days: 30)),
        effectiveEndsAt: endsAt,
      );

      expect(
          entitlement
              .grantsAccessAt(endsAt.subtract(const Duration(microseconds: 1))),
          isTrue);
      expect(entitlement.grantsAccessAt(endsAt), isFalse);
      expect(entitlement.grantsAccessAt(endsAt.add(const Duration(hours: 1))),
          isFalse);
    });

    test('escapes customer identifiers as one path segment', () async {
      late Uri captured;
      final client = IapStackClient(
        _config(),
        httpClient: MockClient((request) async {
          captured = request.url;
          return http.Response(
            jsonEncode(<String, Object?>{
              'customer_id': 'customer-internal',
              'entitlements': _verificationJson['entitlements'],
            }),
            200,
          );
        }),
      );

      await client.getEntitlements('customer/with space');

      expect(captured.pathSegments, contains('customer/with space'));
      expect(captured.toString(), contains('customer%2Fwith%20space'));
    });

    test('retries transient responses and returns the eventual result',
        () async {
      var attempts = 0;
      final client = IapStackClient(
        _config(
          retryPolicy: const IapStackRetryPolicy(
            maxAttempts: 2,
            baseDelay: Duration.zero,
            maxDelay: Duration.zero,
          ),
        ),
        httpClient: MockClient((request) async {
          attempts++;
          if (attempts == 1) {
            return http.Response(
              jsonEncode(<String, Object?>{
                'error': <String, Object?>{
                  'code': 'provider_unavailable',
                  'message': 'provider is unavailable',
                  'request_id': 'request-server-1',
                },
              }),
              503,
            );
          }
          return http.Response(jsonEncode(_verificationJson), 200);
        }),
      );

      final result = await client.verifyPurchase(_purchase());

      expect(attempts, 2);
      expect(result.customerId, 'customer-internal');
    });

    test('exposes stable API errors without response payloads', () async {
      final client = IapStackClient(
        _config(retryPolicy: const IapStackRetryPolicy(maxAttempts: 1)),
        httpClient: MockClient((request) async => http.Response(
              jsonEncode(<String, Object?>{
                'error': <String, Object?>{
                  'code': 'invalid_purchase',
                  'message': 'purchase evidence could not be verified',
                  'request_id': 'request-server-2',
                },
                'secret': 'must-not-escape',
              }),
              422,
            )),
      );

      await expectLater(
        client.verifyPurchase(_purchase()),
        throwsA(
          isA<IapStackApiException>()
              .having((error) => error.statusCode, 'statusCode', 422)
              .having((error) => error.code, 'code', 'invalid_purchase')
              .having(
                  (error) => error.requestId, 'requestId', 'request-server-2')
              .having((error) => error.retryable, 'retryable', isFalse)
              .having((error) => error.toString(), 'safe string',
                  isNot(contains('must-not-escape'))),
        ),
      );
    });

    test('rejects oversized and invalid JSON responses', () async {
      final oversized = IapStackClient(
        _config(maxResponseBytes: 8),
        httpClient: MockClient(
            (request) async => http.Response('{"value":"too-large"}', 200)),
      );
      final invalid = IapStackClient(
        _config(),
        httpClient: MockClient(
            (request) async => http.Response('<html>proxy error</html>', 200)),
      );

      await expectLater(oversized.verifyPurchase(_purchase()),
          throwsA(isA<IapStackProtocolException>()));
      await expectLater(invalid.verifyPurchase(_purchase()),
          throwsA(isA<IapStackProtocolException>()));
    });

    test('turns a bounded attempt deadline into a timeout exception', () async {
      final client = IapStackClient(
        _config(
          timeout: const Duration(milliseconds: 1),
          retryPolicy: const IapStackRetryPolicy(maxAttempts: 1),
        ),
        httpClient: MockClient((request) async {
          await Future<void>.delayed(const Duration(milliseconds: 20));
          return http.Response(jsonEncode(_verificationJson), 200);
        }),
      );

      await expectLater(client.verifyPurchase(_purchase()),
          throwsA(isA<IapStackTimeoutException>()));
    });

    test('retries and redacts transport exceptions outside package:http',
        () async {
      var attempts = 0;
      final client = IapStackClient(
        _config(
          retryPolicy: const IapStackRetryPolicy(
            maxAttempts: 2,
            baseDelay: Duration.zero,
            maxDelay: Duration.zero,
          ),
        ),
        httpClient: MockClient((request) async {
          attempts++;
          throw Exception('private TLS diagnostics');
        }),
      );

      await expectLater(
        client.verifyPurchase(_purchase()),
        throwsA(
          isA<IapStackTransportException>().having(
            (error) => error.toString(),
            'safe string',
            isNot(contains('private TLS diagnostics')),
          ),
        ),
      );
      expect(attempts, 2);
    });

    test('aborts the underlying HTTP request when an attempt times out',
        () async {
      final transport = _AbortTrackingClient();
      final client = IapStackClient(
        _config(
          timeout: const Duration(milliseconds: 1),
          retryPolicy: const IapStackRetryPolicy(maxAttempts: 1),
        ),
        httpClient: transport,
      );

      await expectLater(client.verifyPurchase(_purchase()),
          throwsA(isA<IapStackTimeoutException>()));
      await transport.aborted.timeout(const Duration(seconds: 1));
    });
  });

  group('IapStackRetryPolicy', () {
    test('applies full jitter within the exponential cap', () {
      const policy = IapStackRetryPolicy(
        baseDelay: Duration(milliseconds: 100),
        maxDelay: Duration(milliseconds: 250),
      );

      expect(policy.delayAfter(1, randomValue: 0), Duration.zero);
      expect(policy.delayAfter(1, randomValue: 0.5),
          const Duration(milliseconds: 50));
      expect(policy.delayAfter(4, randomValue: 1),
          const Duration(milliseconds: 250));
      expect(() => policy.delayAfter(1, randomValue: 1.1), throwsArgumentError);
    });

    test('raises the jitter delay to a budgeted Retry-After', () {
      const policy = IapStackRetryPolicy(
        baseDelay: Duration(milliseconds: 100),
        maxDelay: Duration(milliseconds: 250),
      );

      expect(policy.delayAfter(1, randomValue: 0.5, retryAfter: null),
          const Duration(milliseconds: 50));
      expect(policy.delayAfter(1, randomValue: 0.5, retryAfter: Duration.zero),
          const Duration(milliseconds: 50));
      expect(
          policy.delayAfter(1,
              randomValue: 1, retryAfter: const Duration(milliseconds: 20)),
          const Duration(milliseconds: 100));
      expect(
          policy.delayAfter(1,
              randomValue: 0, retryAfter: const Duration(seconds: 4)),
          const Duration(seconds: 4));
      expect(
          policy.delayAfter(1,
              randomValue: 1, retryAfter: const Duration(hours: 1)),
          IapStackRetryPolicy.maxRetryAfter);
      expect(IapStackRetryPolicy.maxRetryAfter, const Duration(seconds: 30));
      expect(
          () => policy.delayAfter(1,
              randomValue: 1.1, retryAfter: const Duration(seconds: 4)),
          throwsArgumentError);

      // The budget bounds the cooldown, never the configured jitter.
      const wide = IapStackRetryPolicy(
        baseDelay: Duration(seconds: 40),
        maxDelay: Duration(seconds: 60),
      );
      expect(
          wide.delayAfter(1,
              randomValue: 1, retryAfter: const Duration(seconds: 1)),
          const Duration(seconds: 40));
    });
  });

  group('IapStackConfig', () {
    test('requires HTTPS unless insecure HTTP is explicit', () {
      expect(
        () => IapStackClient(
          IapStackConfig(
            baseUri: Uri.parse('http://iap.example'),
            applicationId: 'application-1',
            customerToken: 'customer-token',
          ),
          httpClient: MockClient((request) async => http.Response('{}', 200)),
        ),
        throwsArgumentError,
      );
    });

    test('redacts invalid bearer values from validation errors', () {
      expect(
        () => IapStackClient(
          IapStackConfig(
            baseUri: Uri.parse('https://iap.example'),
            applicationId: 'application-1',
            customerToken: 'secret with spaces',
          ),
          httpClient: MockClient((request) async => http.Response('{}', 200)),
        ),
        throwsA(isA<ArgumentError>().having((error) => error.toString(),
            'message', isNot(contains('secret with spaces')))),
      );
    });
  });
}

IapStackConfig _config({
  Duration timeout = const Duration(seconds: 1),
  IapStackRetryPolicy retryPolicy = const IapStackRetryPolicy(maxAttempts: 1),
  int maxResponseBytes = 1024 * 1024,
}) =>
    IapStackConfig(
      baseUri: Uri.parse('https://iap.example/proxy'),
      applicationId: 'application-1',
      customerToken: 'customer-token',
      timeout: timeout,
      retryPolicy: retryPolicy,
      maxResponseBytes: maxResponseBytes,
    );

PurchaseSubmission _purchase([String productId = 'premium_lifetime']) =>
    PurchaseSubmission(
      externalCustomerId: 'customer-external',
      claimedProducts: <String>[productId],
      evidence: <String, Object?>{
        'purchase_data': '{"productId":"$productId"}',
        'signature': 'signed',
        'product_kind': 'non_consumable',
      },
    );

final Map<String, Object?> _verificationJson = <String, Object?>{
  'verified_at': '2026-08-24T20:00:00Z',
  'customer_id': 'customer-internal',
  'entitlements': <Object?>[
    <String, Object?>{
      'key': 'premium',
      'access': 'allowed',
      'reason': 'purchase_valid',
      'effective_starts_at': '2026-08-24T19:00:00Z',
      'version': 1,
    },
  ],
};

final class _AbortTrackingClient extends http.BaseClient {
  final Completer<void> _aborted = Completer<void>();

  Future<void> get aborted => _aborted.future;

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    if (request is! http.Abortable || request.abortTrigger == null) {
      throw StateError('request was not abortable');
    }
    await request.abortTrigger;
    if (!_aborted.isCompleted) {
      _aborted.complete();
    }
    throw http.RequestAbortedException(request.url);
  }
}
