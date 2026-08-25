import 'dart:async';
import 'dart:convert';
import 'dart:math';
import 'dart:typed_data';

import 'package:http/http.dart' as http;
import 'package:iapstack/src/config.dart';
import 'package:iapstack/src/errors.dart';
import 'package:iapstack/src/models.dart';

const String _sdkVersion = '0.1.0-dev.1';

/// Application-scoped client for the IAPStack v1 API.
final class IapStackClient {
  /// Creates a client, optionally using an injected HTTP transport for testing.
  IapStackClient(IapStackConfig config, {http.Client? httpClient})
      : _config = config,
        _httpClient = httpClient ?? http.Client(),
        _ownsHttpClient = httpClient == null,
        _retryRandom = Random() {
    config.validate();
  }

  final IapStackConfig _config;
  final http.Client _httpClient;
  final bool _ownsHttpClient;
  final Random _retryRandom;

  /// Verifies one signed store purchase and returns current projections.
  Future<VerificationResult> verifyPurchase(
    PurchaseSubmission purchase, {
    String? requestId,
  }) async {
    _validatePurchase(purchase);
    final json = await _request(
      method: 'POST',
      path: <String>[
        'v1',
        'applications',
        _config.applicationId,
        'purchases:verify'
      ],
      body: purchase.toJson(),
      requestId: requestId,
    );
    return _decode(() => VerificationResult.fromJson(json));
  }

  /// Verifies between one and 100 signed store purchases in request order.
  Future<RestoreResult> restorePurchases(
    List<PurchaseSubmission> purchases, {
    String? requestId,
  }) async {
    if (purchases.isEmpty || purchases.length > 100) {
      throw ArgumentError.value(purchases.length, 'purchases',
          'must contain between 1 and 100 items');
    }
    for (final purchase in purchases) {
      _validatePurchase(purchase);
    }
    final json = await _request(
      method: 'POST',
      path: <String>[
        'v1',
        'applications',
        _config.applicationId,
        'purchases:restore'
      ],
      body: <String, Object?>{
        'purchases': purchases
            .map((purchase) => purchase.toJson())
            .toList(growable: false),
      },
      requestId: requestId,
    );
    return _decode(() => RestoreResult.fromJson(json));
  }

  /// Loads the current projection snapshot for one external customer.
  Future<EntitlementSnapshot> getEntitlements(
    String externalCustomerId, {
    String? requestId,
  }) async {
    if (externalCustomerId.trim().isEmpty) {
      throw ArgumentError.value(
          externalCustomerId, 'externalCustomerId', 'must not be empty');
    }
    final json = await _request(
      method: 'GET',
      path: <String>[
        'v1',
        'applications',
        _config.applicationId,
        'customers',
        externalCustomerId,
        'entitlements',
      ],
      requestId: requestId,
    );
    return _decode(() => EntitlementSnapshot.fromJson(json));
  }

  /// Releases the internally-created HTTP transport.
  void close() {
    if (_ownsHttpClient) {
      _httpClient.close();
    }
  }

  Future<Map<String, Object?>> _request({
    required String method,
    required List<String> path,
    Map<String, Object?>? body,
    String? requestId,
  }) async {
    final uri = _config.baseUri.replace(
      pathSegments: <String>[
        ..._config.baseUri.pathSegments.where((segment) => segment.isNotEmpty),
        ...path,
      ],
    );
    for (var attempt = 1;
        attempt <= _config.retryPolicy.maxAttempts;
        attempt++) {
      _RawResponse response;
      final abortTrigger = Completer<void>();
      try {
        response = await _send(
          method,
          uri,
          body,
          requestId,
          abortTrigger.future,
        ).timeout(
          _config.timeout,
          onTimeout: () {
            abortTrigger.complete();
            throw TimeoutException(
                'IAPStack HTTP attempt exceeded its deadline');
          },
        );
      } on TimeoutException catch (error) {
        if (attempt < _config.retryPolicy.maxAttempts) {
          await _delay(attempt);
          continue;
        }
        throw IapStackTimeoutException('IAPStack request timed out',
            cause: error);
      } on http.ClientException catch (error) {
        if (attempt < _config.retryPolicy.maxAttempts) {
          await _delay(attempt);
          continue;
        }
        throw IapStackTransportException(
            'IAPStack request failed before a response was received',
            cause: error);
      }

      if (response.statusCode >= 200 && response.statusCode < 300) {
        return _decodeObject(response.body);
      }
      final exception = _apiException(response);
      if (exception.retryable && attempt < _config.retryPolicy.maxAttempts) {
        await _delay(attempt);
        continue;
      }
      throw exception;
    }
    throw const IapStackTransportException(
        'IAPStack request exhausted its retry policy');
  }

  Future<_RawResponse> _send(
    String method,
    Uri uri,
    Map<String, Object?>? body,
    String? requestId,
    Future<void> abortTrigger,
  ) async {
    final request = http.AbortableRequest(
      method,
      uri,
      abortTrigger: abortTrigger,
    )
      ..headers['Accept'] = 'application/json'
      ..headers['Authorization'] = 'Bearer ${_config.customerToken}'
      ..headers['X-IAPStack-SDK'] = 'flutter/$_sdkVersion';
    if (requestId != null && requestId.trim().isNotEmpty) {
      request.headers['X-Request-ID'] = requestId.trim();
    }
    if (body != null) {
      request.headers['Content-Type'] = 'application/json';
      request.bodyBytes = utf8.encode(jsonEncode(body));
    }
    final response = await _httpClient.send(request);
    final bytes = await _collect(response.stream);
    return _RawResponse(
      statusCode: response.statusCode,
      headers: response.headers,
      body: utf8.decode(bytes),
    );
  }

  Future<Uint8List> _collect(Stream<List<int>> stream) async {
    final builder = BytesBuilder(copy: false);
    var length = 0;
    await for (final chunk in stream) {
      length += chunk.length;
      if (length > _config.maxResponseBytes) {
        throw const IapStackProtocolException(
            'IAPStack response exceeded the configured size limit');
      }
      builder.add(chunk);
    }
    return builder.takeBytes();
  }

  Future<void> _delay(int attempt) => Future<void>.delayed(
        _config.retryPolicy.delayAfter(
          attempt,
          randomValue: _retryRandom.nextDouble(),
        ),
      );

  IapStackApiException _apiException(_RawResponse response) {
    var code = 'http_error';
    var message = 'IAPStack returned an unsuccessful response';
    String? requestId = response.headers['x-request-id'];
    try {
      final envelope = _decodeObject(response.body);
      final error = envelope['error'];
      if (error is Map<String, Object?>) {
        final parsedCode = error['code'];
        final parsedMessage = error['message'];
        final parsedRequestId = error['request_id'];
        if (parsedCode is String && parsedCode.isNotEmpty) {
          code = parsedCode;
        }
        if (parsedMessage is String && parsedMessage.isNotEmpty) {
          message = parsedMessage;
        }
        if (parsedRequestId is String && parsedRequestId.isNotEmpty) {
          requestId = parsedRequestId;
        }
      }
    } on IapStackProtocolException {
      // Preserve the safe generic error when a proxy returns non-JSON content.
    }
    return IapStackApiException(
      statusCode: response.statusCode,
      code: code,
      message: message,
      requestId: requestId,
      retryable: response.statusCode == 429 || response.statusCode >= 500,
    );
  }

  Map<String, Object?> _decodeObject(String body) {
    try {
      final value = jsonDecode(body);
      if (value is! Map<String, Object?>) {
        throw const FormatException('root JSON value must be an object');
      }
      return value;
    } on FormatException catch (error) {
      throw IapStackProtocolException(
          'IAPStack returned an invalid JSON object',
          cause: error);
    }
  }

  T _decode<T>(T Function() decode) {
    try {
      return decode();
    } on FormatException catch (error) {
      throw IapStackProtocolException(
          'IAPStack response did not match the v1 contract',
          cause: error);
    }
  }

  void _validatePurchase(PurchaseSubmission purchase) {
    if (purchase.externalCustomerId.trim().isEmpty ||
        purchase.claimedProducts.isEmpty ||
        purchase.claimedProducts.any((product) => product.trim().isEmpty) ||
        purchase.evidence.isEmpty) {
      throw ArgumentError.value(
          purchase, 'purchase', 'contains empty required values');
    }
    final uniqueProducts = purchase.claimedProducts.toSet();
    if (uniqueProducts.length != purchase.claimedProducts.length) {
      throw ArgumentError.value(purchase.claimedProducts, 'claimedProducts',
          'must not contain duplicates');
    }
  }
}

final class _RawResponse {
  const _RawResponse(
      {required this.statusCode, required this.headers, required this.body});

  final int statusCode;
  final Map<String, String> headers;
  final String body;
}
