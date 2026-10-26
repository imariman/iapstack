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

  /// Whether the current authoritative projection permits access now.
  bool get grantsAccess => grantsAccessAt(DateTime.now());

  /// Whether the projection permits access at the supplied instant.
  ///
  /// Only [access] and [effectiveEndsAt] take part. [effectiveStartsAt] is
  /// informational: the server only emits an allowed projection once its
  /// period has started, and checking it here would let a device clock that
  /// runs behind the server deny access right after a purchase. Every
  /// IAPStack SDK applies this rule.
  bool grantsAccessAt(DateTime instant) {
    if (access != 'allowed') {
      return false;
    }
    final endsAt = effectiveEndsAt;
    return endsAt == null || instant.toUtc().isBefore(endsAt);
  }

  /// Decodes one v1 entitlement object.
  factory Entitlement.fromJson(Map<String, Object?> json) => Entitlement(
        key: _requiredString(json, 'key'),
        access: _requiredString(json, 'access'),
        reason: _requiredString(json, 'reason'),
        version: _requiredPositiveInt(json, 'version'),
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

/// Public offering document for one application.
///
/// The document names store products. It does not carry a price, currency,
/// credential, or customer.
final class OfferingDocument {
  /// Creates one schema version 1 offering document.
  OfferingDocument({
    required this.schemaVersion,
    required this.applicationId,
    required this.revision,
    required List<Offering> offerings,
  }) : offerings = List<Offering>.unmodifiable(offerings) {
    if (schemaVersion != 1 ||
        applicationId.isEmpty ||
        revision.isEmpty ||
        offerings.isEmpty) {
      throw ArgumentError('OfferingDocument is incomplete');
    }
  }

  /// Document shape. Only version 1 is accepted.
  final int schemaVersion;

  /// Application whose configured store products are named.
  final String applicationId;

  /// Revision token used as the response ETag.
  final String revision;

  /// Named package groups.
  final List<Offering> offerings;

  /// Decodes the public v1 offering response.
  factory OfferingDocument.fromJson(Map<String, Object?> json) {
    final version = json['schema_version'];
    if (version is! int || version != 1) {
      throw const FormatException('schema_version must be 1');
    }
    final offerings = _objectList(json, 'offerings')
        .map(Offering.fromJson)
        .toList(growable: false);
    if (offerings.isEmpty) {
      throw const FormatException('offerings must not be empty');
    }
    return OfferingDocument(
      schemaVersion: version,
      applicationId: _requiredString(json, 'application_id'),
      revision: _requiredString(json, 'revision'),
      offerings: offerings,
    );
  }
}

/// One named group of store packages.
final class Offering {
  /// Creates an offering group.
  Offering({required this.id, required List<OfferingPackage> packages})
      : packages = List<OfferingPackage>.unmodifiable(packages) {
    if (id.isEmpty || packages.isEmpty) {
      throw ArgumentError('Offering is incomplete');
    }
  }

  /// Stable offering identifier.
  final String id;

  /// Packages a client may present.
  final List<OfferingPackage> packages;

  /// Decodes one offering group.
  factory Offering.fromJson(Map<String, Object?> json) {
    final packages = _objectList(json, 'packages')
        .map(OfferingPackage.fromJson)
        .toList(growable: false);
    if (packages.isEmpty) {
      throw const FormatException('packages must not be empty');
    }
    return Offering(id: _requiredString(json, 'id'), packages: packages);
  }
}

/// One store product a client may present.
final class OfferingPackage {
  /// Creates one offering package.
  OfferingPackage({
    required this.id,
    required this.storeProductId,
    this.displayKey,
  }) {
    if (id.isEmpty || storeProductId.isEmpty) {
      throw ArgumentError('OfferingPackage is incomplete');
    }
  }

  /// Stable package identifier.
  final String id;

  /// Provider product identifier already configured for the application.
  final String storeProductId;

  /// Optional client display key. Prices stay on the store.
  final String? displayKey;

  /// Decodes one offering package.
  factory OfferingPackage.fromJson(Map<String, Object?> json) =>
      OfferingPackage(
        id: _requiredString(json, 'id'),
        storeProductId: _requiredString(json, 'store_product_id'),
        displayKey: _optionalNonEmptyString(json, 'display_key'),
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

String? _optionalNonEmptyString(Map<String, Object?> json, String key) {
  if (!json.containsKey(key) || json[key] == null) {
    return null;
  }
  final value = json[key];
  if (value is! String || value.isEmpty) {
    throw FormatException('$key must be a non-empty string');
  }
  return value;
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

int _requiredPositiveInt(Map<String, Object?> json, String key) {
  final value = _requiredInt(json, key);
  if (value < 1) {
    throw FormatException('$key must be at least 1');
  }
  return value;
}

DateTime _requiredDateTime(Map<String, Object?> json, String key) {
  final value = _requiredString(json, key);
  return _parseDateTime(value, key);
}

DateTime? _optionalDateTime(Map<String, Object?> json, String key) {
  final value = json[key];
  if (value == null) {
    return null;
  }
  if (value is! String || value.isEmpty) {
    throw FormatException('$key must be an RFC 3339 date-time');
  }
  return _parseDateTime(value, key);
}

/// Rejects local dates and impossible calendar fields before Dart normalizes them.
DateTime _parseDateTime(String value, String key) {
  final match = RegExp(
    r'^(\d{4})-(\d{2})-(\d{2})[Tt](\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(?:[Zz]|[+-](\d{2}):(\d{2}))$',
  ).firstMatch(value);
  if (match == null) {
    throw FormatException('$key must be an RFC 3339 date-time');
  }
  final fields = <int>[
    for (var index = 1; index <= 8; index++)
      int.parse(match.group(index) ?? '0'),
  ];
  final calendar = DateTime.utc(fields[0], fields[1], fields[2]);
  if (calendar.year != fields[0] ||
      calendar.month != fields[1] ||
      calendar.day != fields[2] ||
      fields[3] > 23 ||
      fields[4] > 59 ||
      fields[5] > 59 ||
      fields[6] > 23 ||
      fields[7] > 59) {
    throw FormatException('$key must be an RFC 3339 date-time');
  }
  return DateTime.parse(value.toUpperCase()).toUtc();
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
