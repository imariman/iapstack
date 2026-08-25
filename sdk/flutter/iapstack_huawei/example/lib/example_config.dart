import 'package:iapstack_huawei/iapstack_huawei.dart';

/// Non-persistent runtime configuration for the sandbox example.
final class ExampleConfig {
  /// Creates an immutable example configuration.
  const ExampleConfig({
    required this.baseUrl,
    required this.applicationId,
    required this.customerToken,
    required this.externalCustomerId,
    required this.productId,
    required this.productKind,
  });

  /// Loads values passed with Flutter `--dart-define` flags.
  factory ExampleConfig.fromEnvironment() => ExampleConfig(
        baseUrl: const String.fromEnvironment('IAPSTACK_BASE_URL'),
        applicationId: const String.fromEnvironment('IAPSTACK_APPLICATION_ID'),
        customerToken: const String.fromEnvironment('IAPSTACK_CUSTOMER_TOKEN'),
        externalCustomerId:
            const String.fromEnvironment('IAPSTACK_EXTERNAL_CUSTOMER_ID'),
        productId: const String.fromEnvironment('IAPSTACK_HUAWEI_PRODUCT_ID'),
        productKind: _productKindFromEnvironment(
          const String.fromEnvironment(
            'IAPSTACK_HUAWEI_PRODUCT_KIND',
            defaultValue: 'subscription',
          ),
        ),
      );

  /// IAPStack API origin.
  final String baseUrl;

  /// Application scope.
  final String applicationId;

  /// Runtime-only customer session bearer.
  final String customerToken;

  /// Current host-application customer binding.
  final String externalCustomerId;

  /// Huawei AppGallery product ID exercised by the example.
  final String productId;

  /// Huawei product kind exercised by the example.
  final HuaweiProductKind productKind;

  /// Whether every required runtime value is present.
  bool get isComplete => missingValues.isEmpty;

  /// Safe names of missing values, never their contents.
  List<String> get missingValues => <String>[
        if (baseUrl.trim().isEmpty) 'IAPSTACK_BASE_URL',
        if (applicationId.trim().isEmpty) 'IAPSTACK_APPLICATION_ID',
        if (customerToken.trim().isEmpty) 'IAPSTACK_CUSTOMER_TOKEN',
        if (externalCustomerId.trim().isEmpty) 'IAPSTACK_EXTERNAL_CUSTOMER_ID',
        if (productId.trim().isEmpty) 'IAPSTACK_HUAWEI_PRODUCT_ID',
      ];
}

HuaweiProductKind _productKindFromEnvironment(String value) => switch (value) {
      'non_consumable' => HuaweiProductKind.nonConsumable,
      _ => HuaweiProductKind.subscription,
    };
