import Foundation
import XCTest

@testable import IAPStackApple

/// Go's `encoding/json` writes `time.Time` as RFC 3339 with up to nine fraction
/// digits and trailing zeros removed. These are the shapes the API returns.
final class ServerTimestampTests: XCTestCase {
  /// 2026-11-01T21:52:49Z
  private let whole: TimeInterval = 1_793_569_969

  private var serverShaped: [(String, TimeInterval)] {
    [
      ("2026-11-01T21:52:49Z", 0),
      ("2026-11-01T21:52:49.2Z", 0.2),
      ("2026-11-01T21:52:49.25Z", 0.25),
      ("2026-11-01T21:52:49.123Z", 0.123),
      ("2026-11-01T21:52:49.1234Z", 0.1234),
      ("2026-11-01T21:52:49.123456Z", 0.123456),
      ("2026-11-01T21:52:49.28412077Z", 0.28412077),
      ("2026-11-01T21:52:49.284120771Z", 0.284120771),
      ("2026-11-02T00:52:49.5+03:00", 0.5),
      ("2026-11-01T14:22:49.999999999-07:30", 0.999999999),
    ]
  }

  func testParsesWholeSecondsAndEveryFractionLength() throws {
    for (value, fraction) in serverShaped {
      let parsed = try XCTUnwrap(parseServerTimestamp(value), value)
      XCTAssertEqual(parsed.timeIntervalSince1970, whole + fraction, accuracy: 0.000_001, value)
    }
  }

  func testKeepsSubMillisecondPrecision() throws {
    let start = try XCTUnwrap(parseServerTimestamp("2026-11-01T21:52:49Z"))
    let later = try XCTUnwrap(parseServerTimestamp("2026-11-01T21:52:49.0001Z"))
    XCTAssertGreaterThan(later, start)
  }

  func testRejectsMalformedTimestamps() {
    for value in [
      "",
      "not-a-timestamp",
      "2026-11-01T21:52:49",
      "2026-11-01T21:52:49.123",
      "2026-11-01T21:52:49.Z",
      "2026-11-01T21:52:49.",
      "2026-11-01T21:52:49.12a3Z",
      "2026-11-01T21:52:49.\u{FF11}\u{FF12}Z",
      "2026-11-01.5T21:52:49Z",
    ] {
      XCTAssertNil(parseServerTimestamp(value), value)
    }
  }

  func testDecodesServerShapedEntitlementVerifyAndRestoreResponses() throws {
    for (value, fraction) in serverShaped {
      let entitlement = """
      {"key":"premium","access":"allowed","reason":"verified","version":2,
       "effective_starts_at":"\(value)","effective_ends_at":"\(value)"}
      """
      let verification = """
      {"verified_at":"\(value)","customer_id":"customer-internal","entitlements":[\(entitlement)]}
      """
      let expected = whole + fraction

      let snapshot = try EntitlementSnapshot.from(object(#"{"customer_id":"c","entitlements":[\#(entitlement)]}"#))
      let projected = try XCTUnwrap(snapshot.entitlements.first, value)
      let startsAt = try XCTUnwrap(projected.effectiveStartsAt, value)
      let endsAt = try XCTUnwrap(projected.effectiveEndsAt, value)
      XCTAssertEqual(startsAt.timeIntervalSince1970, expected, accuracy: 0.000_001, value)
      XCTAssertEqual(endsAt.timeIntervalSince1970, expected, accuracy: 0.000_001, value)

      let verified = try VerificationResult.from(object(verification))
      XCTAssertEqual(verified.verifiedAt.timeIntervalSince1970, expected, accuracy: 0.000_001, value)

      let restored = try RestoreResult.from(object(#"{"results":[\#(verification),\#(verification)]}"#))
      XCTAssertEqual(restored.results.count, 2, value)
      for row in restored.results {
        XCTAssertEqual(row.verifiedAt.timeIntervalSince1970, expected, accuracy: 0.000_001, value)
      }
    }
  }

  func testEntitlementEndingWithinTheSecondStillGrantsAccessBeforeTheEnd() throws {
    let snapshot = try EntitlementSnapshot.from(object("""
    {"customer_id":"c","entitlements":[{"key":"premium","access":"allowed","reason":"verified","version":1,
     "effective_starts_at":"2026-11-01T21:52:48.284120771Z","effective_ends_at":"2026-11-01T21:52:49.75Z"}]}
    """))
    let entitlement = try XCTUnwrap(snapshot.entitlements.first)
    XCTAssertTrue(entitlement.grantsAccess(at: Date(timeIntervalSince1970: whole + 0.5)))
    XCTAssertFalse(entitlement.grantsAccess(at: Date(timeIntervalSince1970: whole + 0.75)))
  }

  func testConcurrentDecodesAgree() throws {
    let cases = serverShaped
    let payloads = try cases.map { value, _ in
      try object("""
      {"verified_at":"\(value)","customer_id":"c","entitlements":[{"key":"premium","access":"allowed",
       "reason":"verified","version":1,"effective_starts_at":"\(value)","effective_ends_at":null}]}
      """)
    }
    let failures = Counter()
    DispatchQueue.concurrentPerform(iterations: 2_000) { index in
      let expected = whole + cases[index % cases.count].1
      guard let result = try? VerificationResult.from(payloads[index % payloads.count]),
        abs(result.verifiedAt.timeIntervalSince1970 - expected) < 0.000_001,
        let startsAt = result.entitlements.first?.effectiveStartsAt,
        abs(startsAt.timeIntervalSince1970 - expected) < 0.000_001
      else {
        failures.increment()
        return
      }
    }
    XCTAssertEqual(failures.value, 0)
  }

  private func object(_ json: String) throws -> [String: Any] {
    try XCTUnwrap(JSONSerialization.jsonObject(with: Data(json.utf8)) as? [String: Any])
  }
}
