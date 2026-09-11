import XCTest

final class OpenAPIContractTests: XCTestCase {
  func testDeclaresOperationsAndFieldsUsedByTheClient() throws {
    let contract = URL(fileURLWithPath: #filePath)
      .deletingLastPathComponent()
      .appendingPathComponent("../../../../contracts/openapi/v1.yaml")
      .standardizedFileURL
    let yaml = try String(contentsOf: contract, encoding: .utf8)

    XCTAssertTrue(yaml.contains("operationId: verifyPurchase"))
    XCTAssertTrue(yaml.contains("operationId: restorePurchases"))
    XCTAssertTrue(yaml.contains("operationId: getCustomerEntitlements"))

    for field in [
      "external_customer_id",
      "claimed_products",
      "evidence",
      "verified_at",
      "customer_id",
      "entitlements",
      "results",
    ] {
      XCTAssertTrue(yaml.contains(field), "missing contract field \(field)")
    }
  }
}
