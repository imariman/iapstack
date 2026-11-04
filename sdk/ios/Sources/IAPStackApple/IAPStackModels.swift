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
    try validateExternalCustomerId(externalCustomerId)
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
  ///
  /// Only `access` and `effectiveEndsAt` take part. `effectiveStartsAt` is
  /// informational: the server only emits an allowed projection once its period
  /// has started, and checking it here would let a device clock that runs behind
  /// the server deny access right after a purchase. Every IAPStack SDK applies
  /// this rule.
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

/// Rows a store restore did not submit.
///
/// Direct HTTP restores leave every count at zero. Duplicate transactions and
/// rows that cannot be verified yet are omitted.
public struct RestoreSkipCounts: Sendable, Equatable {
  /// Creates skip counts. Omitted counts are zero.
  public init(
    unbound: Int = 0,
    differentCustomer: Int = 0,
    unknownProduct: Int = 0,
    multipleProducts: Int = 0,
  ) {
    self.unbound = unbound
    self.differentCustomer = differentCustomer
    self.unknownProduct = unknownProduct
    self.multipleProducts = multipleProducts
  }

  /// Purchases with no store account binding.
  public let unbound: Int

  /// Purchases bound to a different customer. These are never submitted.
  public let differentCustomer: Int

  /// Purchases whose product is not in the local catalog.
  public let unknownProduct: Int

  /// Play purchases that list more than one product id.
  public let multipleProducts: Int

  /// Sum of the reason counts.
  public var total: Int {
    unbound + differentCustomer + unknownProduct + multipleProducts
  }
}

/// Batched restore result.
///
/// `skipped` and `pageLimitReached` are client-side. `from` leaves them empty
/// because the v1 body only contains `results`. `pageLimitReached` is unused by
/// the Apple companion and stays false.
public struct RestoreResult: Sendable {
  /// Creates one restore response.
  public init(
    results: [VerificationResult],
    skipped: RestoreSkipCounts = RestoreSkipCounts(),
    pageLimitReached: Bool = false,
  ) {
    self.results = results
    self.skipped = skipped
    self.pageLimitReached = pageLimitReached
  }

  /// Per-purchase verification rows.
  public let results: [VerificationResult]

  /// Store rows that were not submitted.
  public let skipped: RestoreSkipCounts

  /// True when a Huawei restore stopped because the page cap was reached.
  public let pageLimitReached: Bool

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
  guard let value = parseServerTimestamp(timestamp) else {
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
  guard let value = parseServerTimestamp(timestamp) else {
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

/// Parses one RFC 3339 timestamp, such as `2026-11-01T21:52:49.28412077Z`.
///
/// Go's `encoding/json` writes `time.Time` with zero to nine fraction digits.
/// `ISO8601DateFormatter` keeps at most milliseconds of a fraction and is not
/// documented to accept nine digits on older OS releases, so the fraction is
/// read here and the rest goes through a whole-second formatter. RFC 3339
/// allows no whitespace, and the formatter would accept leading or trailing
/// whitespace only on some shapes, so any whitespace is rejected.
func parseServerTimestamp(_ value: String) -> Date? {
  guard !value.contains(where: \.isWhitespace) else { return nil }
  let formatter = IAPStackDateFormatter.wholeSeconds
  // A fraction can only follow the seconds of `yyyy-MM-ddTHH:mm:ss`.
  guard let dot = value.index(value.startIndex, offsetBy: 19, limitedBy: value.endIndex),
    dot < value.endIndex, value[dot] == "."
  else {
    return formatter.date(from: value)
  }
  let digits = value[value.index(after: dot)...].prefix { $0.isASCII && $0.isNumber }
  guard !digits.isEmpty, let fraction = TimeInterval("0.\(digits)"),
    let wholeSeconds = formatter.date(from: String(value[..<dot]) + value[digits.endIndex...])
  else {
    return nil
  }
  return wholeSeconds.addingTimeInterval(fraction)
}

/// Formats a date with the millisecond precision a JavaScript `Date` keeps.
///
/// `rounding` picks the direction of the cut, so a caller can keep an access
/// end from moving later (`.down`) or a start from moving earlier (`.up`). The
/// formatter alone rounds to the nearest millisecond, which can do either.
func millisecondTimestamp(_ date: Date, rounding rule: FloatingPointRoundingRule) -> String {
  // A `Date` near today holds about a quarter microsecond, so rounding to whole
  // microseconds first keeps an exact millisecond value from shifting by one.
  let microseconds = (date.timeIntervalSince1970 * 1_000_000).rounded()
  let milliseconds = (microseconds / 1000).rounded(rule)
  return IAPStackDateFormatter.milliseconds.string(from: Date(timeIntervalSince1970: milliseconds / 1000))
}

/// Shared formatters. Their options never change after initialization, and
/// Foundation date formatters are thread-safe, so concurrent decodes can share them.
private enum IAPStackDateFormatter {
  static let wholeSeconds = ISO8601DateFormatter()
  static let milliseconds: ISO8601DateFormatter = {
    let formatter = ISO8601DateFormatter()
    formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    return formatter
  }()
}

/// Requires one non-empty customer identity without surrounding whitespace; a
/// padded ID would silently address a different customer server-side.
func validateExternalCustomerId(_ value: String) throws {
  if value.isEmpty || value.trimmingCharacters(in: .whitespacesAndNewlines) != value {
    throw IAPStackSDKError.configurationError(
      message: "externalCustomerId must be non-empty without surrounding whitespace",
    )
  }
}
