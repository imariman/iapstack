import Foundation
import XCTest

@testable import IAPStackApple

/// Same cases as `GooglePlayIapStackTest` covers for the Android rule.
final class PurchaseListenerEventTypeTests: XCTestCase {
  private func entitlement(
    _ key: String,
    _ access: String,
    _ reason: String,
    endsAt: Date? = nil,
  ) -> Entitlement {
    Entitlement(
      key: key,
      access: access,
      reason: reason,
      version: 1,
      effectiveStartsAt: nil,
      effectiveEndsAt: endsAt,
    )
  }

  func testEmptySnapshotIsPending() {
    XCTAssertEqual(purchaseListenerEventType([]), "pending")
  }

  func testUnresolvedOnlyIsPending() {
    XCTAssertEqual(
      purchaseListenerEventType([entitlement("premium", "unresolved", "pending_payment")]),
      "pending",
    )
    XCTAssertEqual(
      purchaseListenerEventType([entitlement("premium", "unresolved", "store_unavailable")]),
      "pending",
    )
  }

  func testAnotherGrantingEntitlementDoesNotVerifyAPendingPurchase() {
    XCTAssertEqual(
      purchaseListenerEventType([
        entitlement("premium", "allowed", "purchase_valid"),
        entitlement("extra", "unresolved", "pending_payment"),
      ]),
      "pending",
    )
  }

  func testUnresolvedRowThatGrantsAccessIsVerified() {
    XCTAssertEqual(
      purchaseListenerEventType([entitlement("premium", "allowed", "pending_payment")]),
      "verified",
    )
  }

  func testAllGrantingIsVerified() {
    XCTAssertEqual(
      purchaseListenerEventType([
        entitlement("premium", "allowed", "purchase_valid"),
        entitlement("extra", "allowed", "active"),
      ]),
      "verified",
    )
  }

  /// A definitive denial is an answer, not a pending payment. The event name is
  /// `verified`, so callers must read `grantsAccess` before unlocking.
  func testDenialOnlyIsVerifiedButGrantsNothing() {
    let denied = [entitlement("premium", "denied", "refunded")]
    XCTAssertEqual(purchaseListenerEventType(denied), "verified")
    XCTAssertFalse(denied[0].grantsAccess())
  }

  func testExpiredAllowedEntitlementIsVerifiedButGrantsNothing() {
    let expired = [entitlement("premium", "allowed", "active", endsAt: Date(timeIntervalSinceNow: -60))]
    XCTAssertEqual(purchaseListenerEventType(expired), "verified")
    XCTAssertFalse(expired[0].grantsAccess())
  }
}
