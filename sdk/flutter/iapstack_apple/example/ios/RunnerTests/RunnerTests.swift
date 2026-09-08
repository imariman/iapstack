import Flutter
import UIKit
import XCTest
@testable import Runner

class RunnerTests: XCTestCase {

  func testRuntimeCustomerTokenClearsEnvironmentAndCanOnlyBeTakenOnce() {
    var clearedNames: [String] = []
    let token = RuntimeCustomerToken.capture(
      environment: [RuntimeCustomerToken.environmentKey: "runtime-token"],
      clearEnvironment: { clearedNames.append($0) })

    XCTAssertEqual(clearedNames, [RuntimeCustomerToken.environmentKey])
    XCTAssertEqual(token.take(), "runtime-token")
    XCTAssertNil(token.take())
  }

}
