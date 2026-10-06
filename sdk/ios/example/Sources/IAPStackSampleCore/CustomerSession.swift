import Foundation
import IAPStackApple

/// Lets tests replace the trusted-host request; the app uses the SDK's `IAPStackSessionLoader`.
public protocol CustomerSessionLoading {
  func load(endpoint: URL, loginToken: String) async throws -> IAPStackCustomerSession
}

extension IAPStackSessionLoader: CustomerSessionLoading {}
