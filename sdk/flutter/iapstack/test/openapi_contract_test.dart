import 'dart:io';

import 'package:iapstack/iapstack.dart';
import 'package:test/test.dart';
import 'package:yaml/yaml.dart';

void main() {
  group('OpenAPI v1 compatibility', () {
    late YamlMap document;

    setUpAll(() async {
      final contract = File('../../../contracts/openapi/v1.yaml');
      expect(await contract.exists(), isTrue,
          reason: 'The canonical repository contract must be available.');
      document = loadYaml(await contract.readAsString()) as YamlMap;
    });

    test('declares every operation used by the provider-neutral client', () {
      final paths = document['paths'] as YamlMap;
      expect(
        _operationId(
          paths,
          '/v1/applications/{application_id}/purchases:verify',
          'post',
        ),
        'verifyPurchase',
      );
      expect(
        _operationId(
          paths,
          '/v1/applications/{application_id}/purchases:restore',
          'post',
        ),
        'restorePurchases',
      );
      expect(
        _operationId(
          paths,
          '/v1/applications/{application_id}/customers/{external_customer_id}/entitlements',
          'get',
        ),
        'getCustomerEntitlements',
      );
    });

    test('keeps request, response, and error fields consumed by the SDK', () {
      final schemas = (document['components'] as YamlMap)['schemas'] as YamlMap;
      _expectRequiredFields(schemas, 'PurchaseSubmission', const <String>{
        'external_customer_id',
        'claimed_products',
        'evidence',
      });
      _expectRequiredFields(schemas, 'VerificationResult', const <String>{
        'verified_at',
        'customer_id',
        'entitlements',
      });
      _expectRequiredFields(
          schemas, 'RestoreResult', const <String>{'results'});
      _expectRequiredFields(schemas, 'EntitlementSnapshot', const <String>{
        'customer_id',
        'entitlements',
      });
      _expectRequiredFields(schemas, 'ErrorEnvelope', const <String>{'error'});
      _expectRequiredFields(schemas, 'ApiError', const <String>{
        'code',
        'message',
      });
    });

    test('declares the Retry-After header the SDK honors', () {
      final responses =
          (document['components'] as YamlMap)['responses'] as YamlMap;
      final unavailable = responses['ServiceUnavailable'] as YamlMap;
      final headers = unavailable['headers'] as YamlMap;
      expect(headers.keys, contains('Retry-After'));
      expect(IapStackRetryPolicy.maxRetryAfter, const Duration(seconds: 30));
    });
  });

  test('shared SDK behavior is documented', () async {
    final document = await File('../../../docs/api-v1.md').readAsString();
    for (final needle in const <String>[
      '## Shared SDK behavior',
      '`max(jitter, Retry-After)` capped at 30 seconds',
      '`grantsAccess`.** Go and Swift expose a method',
      '`effective_starts_at` is informational',
    ]) {
      expect(document, contains(needle),
          reason: 'shared SDK behavior is missing $needle');
    }
  });
}

String _operationId(YamlMap paths, String path, String method) {
  final pathItem = paths[path] as YamlMap;
  final operation = pathItem[method] as YamlMap;
  return operation['operationId'] as String;
}

void _expectRequiredFields(
  YamlMap schemas,
  String schemaName,
  Set<String> expected,
) {
  final schema = schemas[schemaName] as YamlMap;
  final properties = schema['properties'] as YamlMap;
  final required = (schema['required'] as YamlList).cast<String>().toSet();
  expect(properties.keys.cast<String>(), containsAll(expected));
  expect(required, containsAll(expected));
}
