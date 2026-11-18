import Foundation
import XCTest

@testable import IAPStackApple

/// An administrator's manual grant reports the `manual_grant` reason and is time-bounded like any projection.
final class ManualGrantReasonTests: XCTestCase {
  /// The reason stays a forward-compatible string and access ends at the grant's end.
  func testManualGrantReasonDecodesAndGrantsAccessUntilTheEnd() throws {
    let entitlement = try Entitlement.from([
      "key": "premium",
      "access": "allowed",
      "reason": "manual_grant",
      "effective_starts_at": "2026-11-18T10:00:00Z",
      "effective_ends_at": "2026-12-18T10:00:00Z",
      "version": 2,
    ])

    XCTAssertEqual(entitlement.reason, "manual_grant")
    XCTAssertTrue(entitlement.grantsAccess(at: Date(timeIntervalSince1970: 1_796_083_200)))
    XCTAssertFalse(entitlement.grantsAccess(at: Date(timeIntervalSince1970: 1_797_588_000)))
  }
}
