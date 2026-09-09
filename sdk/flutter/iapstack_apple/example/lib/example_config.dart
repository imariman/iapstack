import 'package:iapstack_apple/iapstack_apple.dart';

/// In-memory configuration for the StoreKit 2 harness.
final class ExampleConfig {
  /// Creates immutable example configuration.
  const ExampleConfig({
    required this.baseUrl,
    required this.applicationId,
    required this.customerToken,
    required this.externalCustomerId,
    required this.subscriptionProductId,
    required this.nonConsumableProductId,
    this.autoDoubleRestore = false,
  });

  /// Loads non-secret values from build defines and accepts the runtime bearer.
  factory ExampleConfig.fromEnvironment({required String customerToken}) =>
      ExampleConfig(
        baseUrl: String.fromEnvironment('IAPSTACK_BASE_URL'),
        applicationId: String.fromEnvironment('IAPSTACK_APPLICATION_ID'),
        customerToken: customerToken,
        externalCustomerId: String.fromEnvironment(
          'IAPSTACK_EXTERNAL_CUSTOMER_ID',
        ),
        subscriptionProductId: String.fromEnvironment(
          'IAPSTACK_APPLE_SUBSCRIPTION_ID',
          defaultValue: 'premium_monthly',
        ),
        nonConsumableProductId: String.fromEnvironment(
          'IAPSTACK_APPLE_NON_CONSUMABLE_ID',
          defaultValue: 'premium_lifetime',
        ),
        autoDoubleRestore: bool.fromEnvironment(
          'IAPSTACK_APPLE_AUTO_DOUBLE_RESTORE',
        ),
      );

  /// IAPStack API origin.
  final String baseUrl;

  /// IAPStack application scope.
  final String applicationId;

  /// Launch-time customer session that is never rendered or persisted.
  final String customerToken;

  /// Canonical UUID used as StoreKit `appAccountToken` and IAPStack customer ID.
  final String externalCustomerId;

  /// App Store auto-renewable subscription identifier.
  final String subscriptionProductId;

  /// App Store non-consumable identifier.
  final String nonConsumableProductId;

  /// Whether startup should execute the real-sandbox restore idempotency check.
  final bool autoDoubleRestore;

  /// Explicit catalog used by the high-level Apple companion.
  Map<String, AppleProductKind> get productKinds => <String, AppleProductKind>{
    if (subscriptionProductId.trim().isNotEmpty)
      subscriptionProductId: AppleProductKind.subscription,
    if (nonConsumableProductId.trim().isNotEmpty)
      nonConsumableProductId: AppleProductKind.nonConsumable,
  };

  /// Whether every required value is present.
  bool get isComplete => missingValues.isEmpty;

  /// Safe names of missing values, never their contents.
  List<String> get missingValues => <String>[
    if (baseUrl.trim().isEmpty) 'IAPSTACK_BASE_URL',
    if (applicationId.trim().isEmpty) 'IAPSTACK_APPLICATION_ID',
    if (customerToken.trim().isEmpty) 'IAPSTACK_CUSTOMER_TOKEN',
    if (externalCustomerId.trim().isEmpty) 'IAPSTACK_EXTERNAL_CUSTOMER_ID',
  ];
}
