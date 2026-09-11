import Foundation

/// Optional customer binding used by some providers.
public struct CustomerBinding: Sendable {
  /// Provider binding name.
  public let kind: String

  /// Signed binding value.
  public let value: String

  /// Creates one expected customer binding.
  public init(kind: String, value: String) {
    self.kind = kind
    self.value = value
  }

  /// Encodes the exact contract payload.
  public func toDictionary() -> [String: String] {
    ["kind": kind, "value": value]
  }
}

/// Provider-signed purchase submission accepted by IAPStack.
public struct PurchaseSubmission: Sendable {
  /// Creates one purchase verification item.
  public init(
    externalCustomerId: String,
    claimedProducts: [String],
    evidence: [String: Any],
    customerBindings: [CustomerBinding] = [],
  ) throws {
    if externalCustomerId.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
      throw IAPStackSDKError.configurationError(message: "externalCustomerId must not be empty")
    }
    if claimedProducts.isEmpty {
      throw IAPStackSDKError.configurationError(
        message: "claimedProducts must contain at least one item",
      )
    }
    let nonEmptyProducts = claimedProducts.filter { !$0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }
    if nonEmptyProducts.count != claimedProducts.count {
      throw IAPStackSDKError.configurationError(
        message: "claimedProducts must not contain blank IDs",
      )
    }
    if Set(claimedProducts).count != claimedProducts.count {
      throw IAPStackSDKError.configurationError(message: "claimedProducts must be unique")
    }
    if evidence.isEmpty {
      throw IAPStackSDKError.configurationError(message: "evidence must not be empty")
    }
    self.externalCustomerId = externalCustomerId
    self.claimedProducts = claimedProducts
    self.evidence = evidence
    self.customerBindings = customerBindings
  }

  /// Application-owned external customer identifier.
  public let externalCustomerId: String

  /// Provider product IDs claimed by signed evidence.
  public let claimedProducts: [String]

  /// Store-specific evidence object.
  public let evidence: [String: Any]

  /// Optional signed customer references for providers.
  public let customerBindings: [CustomerBinding]

  /// Encodes the exact v1 request payload.
  public func toDictionary() -> [String: Any] {
    var value: [String: Any] = [
      "external_customer_id": externalCustomerId,
      "claimed_products": claimedProducts,
      "evidence": evidence,
    ]
    if !customerBindings.isEmpty {
      value["customer_bindings"] = customerBindings.map { $0.toDictionary() }
    }
    return value
  }
}

/// One current entitlement projection.
public struct Entitlement: Sendable {
  /// Creates one immutable entitlement.
  public init(
    key: String,
    access: String,
    reason: String,
    version: Int,
    effectiveStartsAt: Date?,
    effectiveEndsAt: Date?,
  ) {
    self.key = key
    self.access = access
    self.reason = reason
    self.version = version
    self.effectiveStartsAt = effectiveStartsAt
    self.effectiveEndsAt = effectiveEndsAt
  }

  /// Application entitlement key.
  public let key: String

  /// Access string: allowed, denied, etc.
  public let access: String

  /// Normalized reason from the projection service.
  public let reason: String

  /// Projection version.
  public let version: Int

  /// Optional projection start timestamp.
  public let effectiveStartsAt: Date?

  /// Optional projection end timestamp.
  public let effectiveEndsAt: Date?

  /// Builds one entitlement from contract JSON.
  public static func from(_ dictionary: [String: Any]) throws -> Entitlement {
    try Entitlement(
      key: try requiredString(dictionary, key: "key"),
      access: try requiredString(dictionary, key: "access"),
      reason: try requiredString(dictionary, key: "reason"),
      version: try requiredInt(dictionary, key: "version"),
      effectiveStartsAt: try optionalDate(dictionary, key: "effective_starts_at"),
      effectiveEndsAt: try optionalDate(dictionary, key: "effective_ends_at"),
    )
  }

  /// Whether access is granted now.
  public func grantsAccess() -> Bool {
    grantsAccess(at: Date())
  }

  /// Whether access is granted at the given instant.
  public func grantsAccess(at instant: Date) -> Bool {
    guard access == "allowed" else {
      return false
    }
    guard let endsAt = effectiveEndsAt else {
      return true
    }
    return instant < endsAt
  }
}

/// Result after a single verification request.
public struct VerificationResult: Sendable {
  /// Creates one verification response.
  public init(verifiedAt: Date, customerId: String, entitlements: [Entitlement]) {
    self.verifiedAt = verifiedAt
    self.customerId = customerId
    self.entitlements = entitlements
  }

  /// Timestamp of authority confirmation.
  public let verifiedAt: Date

  /// Internal IAPStack customer identifier.
  public let customerId: String

  /// Entitlements derived from verification.
  public let entitlements: [Entitlement]

  /// Builds one result from contract JSON.
  public static func from(_ dictionary: [String: Any]) throws -> VerificationResult {
    try VerificationResult(
      verifiedAt: try requiredDate(dictionary, key: "verified_at"),
      customerId: try requiredString(dictionary, key: "customer_id"),
      entitlements: try requiredObjectList(dictionary, key: "entitlements").map(Entitlement.from),
    )
  }
}

/// Batched restore result.
public struct RestoreResult: Sendable {
  /// Creates one restore response.
  public init(results: [VerificationResult]) {
    self.results = results
  }

  /// Per-purchase verification rows.
  public let results: [VerificationResult]

  /// Builds one batch response from contract JSON.
  public static func from(_ dictionary: [String: Any]) throws -> RestoreResult {
    try RestoreResult(
      results: try requiredObjectList(dictionary, key: "results").map(VerificationResult.from),
    )
  }
}

/// Current entitlement snapshot for one external customer.
public struct EntitlementSnapshot: Sendable {
  /// Creates one snapshot payload.
  public init(customerId: String, entitlements: [Entitlement]) {
    self.customerId = customerId
    self.entitlements = entitlements
  }

  /// Internal durable customer identifier.
  public let customerId: String

  /// Current entitlement set.
  public let entitlements: [Entitlement]

  /// Builds one snapshot from contract JSON.
  public static func from(_ dictionary: [String: Any]) throws -> EntitlementSnapshot {
    try EntitlementSnapshot(
      customerId: try requiredString(dictionary, key: "customer_id"),
      entitlements: try requiredObjectList(dictionary, key: "entitlements").map(Entitlement.from),
    )
  }
}

private func requiredString(_ dictionary: [String: Any], key: String) throws -> String {
  guard let value = dictionary[key], let string = value as? String, !string.isEmpty else {
    throw IAPStackSDKError.protocolError(message: "\(key) must be a non-empty string")
  }
  return string
}

private func requiredInt(_ dictionary: [String: Any], key: String) throws -> Int {
  guard let value = dictionary[key] else {
    throw IAPStackSDKError.protocolError(message: "\(key) must be an integer")
  }
  if let intValue = value as? Int {
    return intValue
  }
  if let longValue = value as? Int64 {
    return Int(longValue)
  }
  if let doubleValue = value as? Double {
    return Int(doubleValue)
  }
  throw IAPStackSDKError.protocolError(message: "\(key) must be an integer")
}

private func requiredDate(_ dictionary: [String: Any], key: String) throws -> Date {
  let timestamp = try requiredString(dictionary, key: key)
  let parsed = IAPStackDateFormatter.shared.date(from: timestamp)
  guard let value = parsed else {
    throw IAPStackSDKError.protocolError(message: "\(key) must be an ISO-8601 timestamp")
  }
  return value
}

private func optionalDate(_ dictionary: [String: Any], key: String) throws -> Date? {
  guard let raw = dictionary[key], !(raw is NSNull) else {
    return nil
  }
  guard let timestamp = raw as? String, !timestamp.isEmpty else {
    throw IAPStackSDKError.protocolError(message: "\(key) must be an ISO-8601 timestamp")
  }
  guard let value = IAPStackDateFormatter.shared.date(from: timestamp) else {
    throw IAPStackSDKError.protocolError(message: "\(key) must be an ISO-8601 timestamp")
  }
  return value
}

private func requiredObjectList(_ dictionary: [String: Any], key: String) throws -> [[String: Any]] {
  guard let raw = dictionary[key] else {
    throw IAPStackSDKError.protocolError(message: "\(key) must be an array")
  }
  guard let list = raw as? [[String: Any]] else {
    throw IAPStackSDKError.protocolError(message: "\(key) must be an array of objects")
  }
  return list
}

private enum IAPStackDateFormatter {
  static let shared = ISO8601DateFormatter()
}
