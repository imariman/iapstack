import 'dart:collection';

/// One expected customer binding included with purchase verification.
final class CustomerBinding {
  /// Creates a provider-owned customer binding.
  const CustomerBinding({required this.kind, required this.value});

  /// Provider binding name.
  final String kind;

  /// Expected signed value.
  final String value;

  /// Encodes the v1 JSON representation.
  Map<String, Object?> toJson() =>
      <String, Object?>{'kind': kind, 'value': value};
}

/// Provider-neutral signed purchase submission accepted by IAPStack.
final class PurchaseSubmission {
  /// Creates one verification item.
  PurchaseSubmission({
    required this.externalCustomerId,
    required List<String> claimedProducts,
    required Map<String, Object?> evidence,
    List<CustomerBinding> customerBindings = const <CustomerBinding>[],
  })  : claimedProducts = List<String>.unmodifiable(claimedProducts),
        evidence = UnmodifiableMapView<String, Object?>(
            Map<String, Object?>.of(evidence)),
        customerBindings = List<CustomerBinding>.unmodifiable(customerBindings);

  /// Application-owned stable customer identifier.
  final String externalCustomerId;

  /// Provider product identifiers claimed by the signed payload.
  final List<String> claimedProducts;

  /// Store-specific, versioned signed evidence object.
  final Map<String, Object?> evidence;

  /// Additional expected signed customer references.
  final List<CustomerBinding> customerBindings;

  /// Encodes the exact public v1 request item.
  Map<String, Object?> toJson() => <String, Object?>{
        'external_customer_id': externalCustomerId,
        'claimed_products': claimedProducts,
        if (customerBindings.isNotEmpty)
          'customer_bindings': customerBindings
              .map((binding) => binding.toJson())
              .toList(growable: false),
        'evidence': evidence,
      };
}

/// One current entitlement projection.
final class Entitlement {
  /// Creates an immutable entitlement projection.
  const Entitlement({
    required this.key,
    required this.access,
    required this.reason,
    required this.version,
    this.effectiveStartsAt,
    this.effectiveEndsAt,
  });

  /// Public entitlement key configured by the application.
  final String key;

  /// Forward-compatible access value, currently allowed, denied, or unresolved.
  final String access;

  /// Forward-compatible normalized access reason.
  final String reason;

  /// Projection generation incremented only for logical changes.
  final int version;

  /// Inclusive access period start, when applicable.
  final DateTime? effectiveStartsAt;

  /// Exclusive access period end, when applicable.
  final DateTime? effectiveEndsAt;

  /// Whether the current authoritative projection permits access.
  bool get grantsAccess => access == 'allowed';

  /// Decodes one v1 entitlement object.
  factory Entitlement.fromJson(Map<String, Object?> json) => Entitlement(
        key: _requiredString(json, 'key'),
        access: _requiredString(json, 'access'),
        reason: _requiredString(json, 'reason'),
        version: _requiredInt(json, 'version'),
        effectiveStartsAt: _optionalDateTime(json, 'effective_starts_at'),
        effectiveEndsAt: _optionalDateTime(json, 'effective_ends_at'),
      );
}

/// Entitlement snapshot returned after one verification.
final class VerificationResult {
  /// Creates a verification result.
  VerificationResult({
    required this.verifiedAt,
    required this.customerId,
    required List<Entitlement> entitlements,
  }) : entitlements = List<Entitlement>.unmodifiable(entitlements);

  /// Server verification timestamp.
  final DateTime verifiedAt;

  /// Internal stable customer identifier.
  final String customerId;

  /// Current projections affected by the purchase.
  final List<Entitlement> entitlements;

  /// Decodes the public v1 verification response.
  factory VerificationResult.fromJson(Map<String, Object?> json) =>
      VerificationResult(
        verifiedAt: _requiredDateTime(json, 'verified_at'),
        customerId: _requiredString(json, 'customer_id'),
        entitlements: _objectList(json, 'entitlements')
            .map(Entitlement.fromJson)
            .toList(growable: false),
      );
}

/// Results returned by one bounded batch restore operation.
final class RestoreResult {
  /// Creates an immutable restore response.
  RestoreResult({required List<VerificationResult> results})
      : results = List<VerificationResult>.unmodifiable(results);

  /// Per-purchase verification snapshots in request order.
  final List<VerificationResult> results;

  /// Decodes the public v1 restore response.
  factory RestoreResult.fromJson(Map<String, Object?> json) => RestoreResult(
        results: _objectList(json, 'results')
            .map(VerificationResult.fromJson)
            .toList(growable: false),
      );
}

/// Current entitlement snapshot for one external customer.
final class EntitlementSnapshot {
  /// Creates an immutable entitlement snapshot.
  EntitlementSnapshot(
      {required this.customerId, required List<Entitlement> entitlements})
      : entitlements = List<Entitlement>.unmodifiable(entitlements);

  /// Internal stable customer identifier.
  final String customerId;

  /// All current application-scoped projections for the customer.
  final List<Entitlement> entitlements;

  /// Decodes the public v1 customer entitlement response.
  factory EntitlementSnapshot.fromJson(Map<String, Object?> json) =>
      EntitlementSnapshot(
        customerId: _requiredString(json, 'customer_id'),
        entitlements: _objectList(json, 'entitlements')
            .map(Entitlement.fromJson)
            .toList(growable: false),
      );
}

String _requiredString(Map<String, Object?> json, String key) {
  final value = json[key];
  if (value is! String || value.isEmpty) {
    throw FormatException('$key must be a non-empty string');
  }
  return value;
}

int _requiredInt(Map<String, Object?> json, String key) {
  final value = json[key];
  if (value is! int) {
    throw FormatException('$key must be an integer');
  }
  return value;
}

DateTime _requiredDateTime(Map<String, Object?> json, String key) {
  final value = _requiredString(json, key);
  return DateTime.parse(value).toUtc();
}

DateTime? _optionalDateTime(Map<String, Object?> json, String key) {
  final value = json[key];
  if (value == null) {
    return null;
  }
  if (value is! String || value.isEmpty) {
    throw FormatException('$key must be an ISO-8601 string');
  }
  return DateTime.parse(value).toUtc();
}

List<Map<String, Object?>> _objectList(Map<String, Object?> json, String key) {
  final value = json[key];
  if (value is! List<Object?>) {
    throw FormatException('$key must be an array');
  }
  return value.map((item) {
    if (item is! Map<String, Object?>) {
      throw FormatException('$key entries must be objects');
    }
    return item;
  }).toList(growable: false);
}
