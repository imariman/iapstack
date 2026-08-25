import 'dart:convert';

import 'package:flutter/services.dart';
import 'package:huawei_iap/huawei_iap.dart';
import 'package:iapstack_huawei/src/errors.dart';
import 'package:iapstack_huawei/src/platform.dart';
import 'package:iapstack_huawei/src/product_kind.dart';

/// Production bridge to Huawei's official `huawei_iap` Flutter plugin.
final class HuaweiPluginPlatform implements HuaweiIapPlatform {
  /// Creates the stateless plugin bridge.
  const HuaweiPluginPlatform();

  @override
  Future<HuaweiSandboxStatus> sandboxStatus() async {
    try {
      final result = await IapClient.isSandboxActivated();
      _requireSuccess(
        result.returnCode,
        result.errMsg,
        operation: 'sandbox check',
      );
      return HuaweiSandboxStatus(
        isSandboxUser: result.isSandboxUser == true,
        isSandboxApk: result.isSandboxApk == true,
        marketVersion: _nonEmpty(result.versionFrMarket),
        apkVersion: _nonEmpty(result.versionInApk),
      );
    } on PlatformException catch (error) {
      throw _platformException(error, operation: 'sandbox_check');
    }
  }

  @override
  Future<HuaweiSignedPurchase> purchase({
    required String productId,
    required HuaweiProductKind productKind,
    required String developerPayload,
  }) async {
    try {
      final result = await IapClient.createPurchaseIntent(
        PurchaseIntentReq(
          priceType: productKind.priceType,
          productId: productId,
          developerPayload: developerPayload,
        ),
      );
      _requireSuccess(result.returnCode, result.errMsg, operation: 'purchase');
      final json = _decodeRawResult(result.rawValue, operation: 'purchase');
      return HuaweiSignedPurchase(
        purchaseData:
            _requiredString(json, 'inAppPurchaseData', operation: 'purchase'),
        signature:
            _requiredString(json, 'inAppDataSignature', operation: 'purchase'),
      );
    } on PlatformException catch (error) {
      throw _platformException(error, operation: 'purchase');
    }
  }

  @override
  Future<HuaweiOwnedPurchasesPage> ownedPurchases({
    required HuaweiProductKind productKind,
    String? continuationToken,
  }) async {
    try {
      final result = await IapClient.obtainOwnedPurchases(
        OwnedPurchasesReq(
          priceType: productKind.priceType,
          continuationToken: continuationToken,
        ),
      );
      _requireSuccess(result.returnCode, result.errMsg, operation: 'restore');
      final json = _decodeRawResult(result.rawValue, operation: 'restore');
      final purchaseData =
          _stringList(json, 'inAppPurchaseDataList', operation: 'restore');
      final signatures =
          _stringList(json, 'inAppSignature', operation: 'restore');
      if (purchaseData.length != signatures.length) {
        throw const HuaweiIapStackException(
          code: 'invalid_plugin_response',
          message: 'Huawei restore data and signature counts differ',
        );
      }
      return HuaweiOwnedPurchasesPage(
        purchases: List<HuaweiSignedPurchase>.generate(
          purchaseData.length,
          (index) => HuaweiSignedPurchase(
            purchaseData: purchaseData[index],
            signature: signatures[index],
          ),
          growable: false,
        ),
        continuationToken: _optionalString(json, 'continuationToken'),
      );
    } on PlatformException catch (error) {
      throw _platformException(error, operation: 'restore');
    }
  }
}

void _requireSuccess(String? code, String? providerMessage,
    {required String operation}) {
  if (code == '0') {
    return;
  }
  throw HuaweiIapStackException(
    code: code?.isNotEmpty == true
        ? code!
        : 'huawei_${operation.replaceAll(' ', '_')}_failed',
    message: providerMessage?.isNotEmpty == true
        ? providerMessage!
        : 'Huawei IAP $operation failed',
    userCancelled: code == '60000',
  );
}

Map<String, Object?> _decodeRawResult(String value,
    {required String operation}) {
  try {
    final decoded = jsonDecode(value);
    if (decoded is! Map<String, Object?>) {
      throw const FormatException('result must be an object');
    }
    return decoded;
  } on FormatException catch (error) {
    throw HuaweiIapStackException(
      code: 'invalid_plugin_response',
      message: 'Huawei IAP $operation returned invalid JSON',
      cause: error,
    );
  }
}

String _requiredString(Map<String, Object?> json, String key,
    {required String operation}) {
  final value = json[key];
  if (value is! String || value.isEmpty) {
    throw HuaweiIapStackException(
      code: 'invalid_plugin_response',
      message: 'Huawei IAP $operation omitted $key',
    );
  }
  return value;
}

String? _optionalString(Map<String, Object?> json, String key) {
  final value = json[key];
  return value is String && value.isNotEmpty ? value : null;
}

String? _nonEmpty(String? value) => value?.isNotEmpty == true ? value : null;

List<String> _stringList(Map<String, Object?> json, String key,
    {required String operation}) {
  final value = json[key];
  if (value == null) {
    return const <String>[];
  }
  if (value is! List<Object?> ||
      value.any((item) => item is! String || item.isEmpty)) {
    throw HuaweiIapStackException(
      code: 'invalid_plugin_response',
      message: 'Huawei IAP $operation returned invalid $key',
    );
  }
  return value.cast<String>();
}

HuaweiIapStackException _platformException(PlatformException error,
        {required String operation}) =>
    HuaweiIapStackException(
      code: error.code.isNotEmpty ? error.code : 'huawei_${operation}_failed',
      message: error.message?.isNotEmpty == true
          ? error.message!
          : 'Huawei IAP $operation failed',
      userCancelled: error.code == '60000',
      cause: error,
    );
