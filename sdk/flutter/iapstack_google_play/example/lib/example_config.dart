import 'package:iapstack_google_play/iapstack_google_play.dart';

/// Non-persistent runtime configuration for the internal-testing harness.
final class ExampleConfig {
  /// Creates immutable example configuration.
  const ExampleConfig({
    required this.baseUrl,
    required this.applicationId,
    required this.customerToken,
    required this.customerSessionBrokerUrl,
    required this.externalCustomerId,
    required this.subscriptionProductId,
    required this.nonConsumableProductId,
  });

  /// Loads values supplied through Flutter `--dart-define` flags.
  factory ExampleConfig.fromEnvironment() => const ExampleConfig(
    baseUrl: String.fromEnvironment('IAPSTACK_BASE_URL'),
    applicationId: String.fromEnvironment('IAPSTACK_APPLICATION_ID'),
    customerToken: String.fromEnvironment('IAPSTACK_CUSTOMER_TOKEN'),
    customerSessionBrokerUrl: String.fromEnvironment(
      'IAPSTACK_CUSTOMER_SESSION_BROKER_URL',
    ),
    externalCustomerId: String.fromEnvironment('IAPSTACK_EXTERNAL_CUSTOMER_ID'),
    subscriptionProductId: String.fromEnvironment(
      'IAPSTACK_GOOGLE_PLAY_SUBSCRIPTION_ID',
    ),
    nonConsumableProductId: String.fromEnvironment(
      'IAPSTACK_GOOGLE_PLAY_NON_CONSUMABLE_ID',
    ),
  );

  /// IAPStack API origin.
  final String baseUrl;

  /// IAPStack application scope.
  final String applicationId;

  /// Runtime-only customer session that is never rendered or persisted.
  final String customerToken;

  /// Optional trusted local broker that mints a fresh customer session.
  final String customerSessionBrokerUrl;

  /// Opaque, non-PII customer binding sent to Google Play and IAPStack.
  final String externalCustomerId;

  /// Optional Google Play subscription exercised by the example.
  final String subscriptionProductId;

  /// Optional Google Play non-consumable exercised by the example.
  final String nonConsumableProductId;

  /// Explicit product catalog used by the high-level companion.
  Map<String, GooglePlayProductKind> get productKinds =>
      <String, GooglePlayProductKind>{
        if (subscriptionProductId.trim().isNotEmpty)
          subscriptionProductId: GooglePlayProductKind.subscription,
        if (nonConsumableProductId.trim().isNotEmpty)
          nonConsumableProductId: GooglePlayProductKind.nonConsumable,
      };

  /// Whether all common values and at least one provider product are present.
  bool get isComplete => missingValues.isEmpty;

  /// Safe names of missing values, never their contents.
  List<String> get missingValues => <String>[
    if (baseUrl.trim().isEmpty) 'IAPSTACK_BASE_URL',
    if (applicationId.trim().isEmpty) 'IAPSTACK_APPLICATION_ID',
    if (customerToken.trim().isEmpty && customerSessionBrokerUrl.trim().isEmpty)
      'IAPSTACK_CUSTOMER_TOKEN or IAPSTACK_CUSTOMER_SESSION_BROKER_URL',
    if (externalCustomerId.trim().isEmpty) 'IAPSTACK_EXTERNAL_CUSTOMER_ID',
    if (subscriptionProductId.trim().isEmpty &&
        nonConsumableProductId.trim().isEmpty)
      'IAPSTACK_GOOGLE_PLAY_SUBSCRIPTION_ID or IAPSTACK_GOOGLE_PLAY_NON_CONSUMABLE_ID',
  ];
}
