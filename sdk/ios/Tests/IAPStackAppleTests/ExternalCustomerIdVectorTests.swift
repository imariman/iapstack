import Foundation
import XCTest

@testable import IAPStackApple

/// Pins the Swift copy of the server's identifier rule to the shared cases in
/// `contracts/test-vectors/external-customer-ids.json`.
final class ExternalCustomerIdVectorTests: XCTestCase {
  private struct Vector {
    let name: String
    let units: [UInt16]
    let accepted: Bool
  }

  func testPurchaseSubmissionFollowsTheSharedServerIdentifierVectors() throws {
    var checked = 0
    var skipped: [String] = []
    for vector in try loadVectors() {
      guard let value = Self.decode(vector.units) else {
        // A Swift string cannot hold an unpaired surrogate: decoding these code
        // units would replace it with U+FFFD, a different ID that the rule
        // accepts. Such an ID cannot reach this SDK, so the case is skipped.
        skipped.append(vector.name)
        XCTAssertFalse(vector.accepted, "\(vector.name): only rejected cases may be unrepresentable")
        continue
      }
      checked += 1
      let submission = try? PurchaseSubmission(
        externalCustomerId: value,
        claimedProducts: ["sku-1"],
        evidence: ["signed_transaction": "a.b.c", "product_kind": "subscription"],
      )
      XCTAssertEqual(submission != nil, vector.accepted, vector.name)
    }
    XCTAssertGreaterThan(checked, 0)
    XCTAssertTrue(skipped.allSatisfy { $0.contains("surrogate") }, "unexpected skips: \(skipped)")
  }

  /// Decodes UTF-16 code units, or returns nil when one is an unpaired surrogate.
  private static func decode(_ units: [UInt16]) -> String? {
    var scalars = String.UnicodeScalarView()
    var iterator = units.makeIterator()
    var decoder = UTF16()
    while true {
      switch decoder.decode(&iterator) {
      case let .scalarValue(scalar):
        scalars.append(scalar)
      case .emptyInput:
        return String(scalars)
      case .error:
        return nil
      }
    }
  }

  /// Loads the shared cases; number segments are UTF-16 code units.
  private func loadVectors() throws -> [Vector] {
    let file = URL(fileURLWithPath: #filePath)
      .deletingLastPathComponent()
      .appendingPathComponent("../../../../contracts/test-vectors/external-customer-ids.json")
      .standardizedFileURL
    let document = try XCTUnwrap(
      JSONSerialization.jsonObject(with: Data(contentsOf: file)) as? [String: Any],
    )
    let cases = try XCTUnwrap(document["cases"] as? [[String: Any]])
    XCTAssertFalse(cases.isEmpty)
    return try cases.map { item in
      var units: [UInt16] = []
      for segment in try XCTUnwrap(item["value"] as? [Any]) {
        if let text = segment as? String {
          units.append(contentsOf: text.utf16)
        } else {
          units.append(UInt16(try XCTUnwrap(segment as? Int)))
        }
      }
      return Vector(
        name: try XCTUnwrap(item["name"] as? String),
        units: units,
        accepted: try XCTUnwrap(item["accepted"] as? Bool),
      )
    }
  }
}
