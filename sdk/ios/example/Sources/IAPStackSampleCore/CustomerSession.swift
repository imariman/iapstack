import Foundation
import IAPStackApple

/// Only short-lived credentials returned by an authenticated, trusted host.
public struct CustomerSession: Decodable {
  public let baseURL: URL
  public let applicationID: String
  public let externalCustomerID: String
  public let token: String
  public let expiresAt: Date

  enum CodingKeys: String, CodingKey {
    case baseURL = "base_url", applicationID = "application_id"
    case externalCustomerID = "external_customer_id", token, expiresAt = "expires_at"
  }

  public init(baseURL: URL, applicationID: String, externalCustomerID: String, token: String, expiresAt: Date) {
    self.baseURL = baseURL
    self.applicationID = applicationID
    self.externalCustomerID = externalCustomerID
    self.token = token
    self.expiresAt = expiresAt
  }

  public var config: IAPStackConfig {
    IAPStackConfig(baseUri: baseURL, applicationId: applicationID, customerToken: token)
  }

  public func validate(now: Date = Date()) throws {
    try config.validate()
    guard let uuid = UUID(uuidString: externalCustomerID),
      uuid.uuidString.lowercased() == externalCustomerID else {
      throw SessionError.invalidResponse
    }
    guard expiresAt > now else { throw SessionError.expired }
  }
}

public enum SessionError: Error {
  case invalidEndpoint, invalidLogin, invalidResponse, expired, unavailable
}

public protocol CustomerSessionLoading {
  func load(endpoint: URL, loginToken: String) async throws -> CustomerSession
}

/// Credentials are kept in memory. Cookies, response caches and redirects are disabled.
public final class TrustedHostSessionLoader: CustomerSessionLoading {
  private let session: URLSession
  private let redirectDelegate = NoRedirect()

  public init() {
    let configuration = URLSessionConfiguration.ephemeral
    configuration.httpShouldSetCookies = false
    configuration.urlCache = nil
    configuration.timeoutIntervalForRequest = 15
    configuration.timeoutIntervalForResource = 20
    session = URLSession(configuration: configuration, delegate: redirectDelegate, delegateQueue: nil)
  }

  /// Test injection; callers must disable redirects on an injected session.
  init(session: URLSession) { self.session = session }

  deinit { session.invalidateAndCancel() }

  public func load(endpoint: URL, loginToken: String) async throws -> CustomerSession {
    guard endpoint.scheme?.lowercased() == "https", let host = endpoint.host, !host.isEmpty,
      endpoint.user == nil, endpoint.password == nil, endpoint.query == nil, endpoint.fragment == nil else {
      throw SessionError.invalidEndpoint
    }
    guard !loginToken.isEmpty, !loginToken.contains(where: { $0.isWhitespace }) else {
      throw SessionError.invalidLogin
    }
    var request = URLRequest(url: endpoint, cachePolicy: .reloadIgnoringLocalCacheData)
    request.httpMethod = "POST"
    request.httpBody = Data("{}".utf8)
    request.setValue("Bearer \(loginToken)", forHTTPHeaderField: "Authorization")
    request.setValue("application/json", forHTTPHeaderField: "Content-Type")
    request.setValue("application/json", forHTTPHeaderField: "Accept")
    let (bytes, response) = try await session.bytes(for: request)
    defer { bytes.task.cancel() }
    guard response.expectedContentLength <= 65_536 else { throw SessionError.invalidResponse }
    guard let http = response as? HTTPURLResponse, http.statusCode == 200 else {
      throw SessionError.unavailable
    }
    var body = Data()
    for try await byte in bytes {
      guard body.count < 65_536 else { throw SessionError.invalidResponse }
      body.append(byte)
    }
    let decoder = JSONDecoder()
    decoder.dateDecodingStrategy = .custom { decoder in
      let text = try decoder.singleValueContainer().decode(String.self)
      let formatter = ISO8601DateFormatter()
      formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
      if let date = formatter.date(from: text) { return date }
      formatter.formatOptions = [.withInternetDateTime]
      guard let date = formatter.date(from: text) else { throw SessionError.invalidResponse }
      return date
    }
    let customerSession = try decoder.decode(CustomerSession.self, from: body)
    try customerSession.validate()
    return customerSession
  }
}

private final class NoRedirect: NSObject, URLSessionTaskDelegate {
  func urlSession(_ session: URLSession, task: URLSessionTask,
    willPerformHTTPRedirection response: HTTPURLResponse, newRequest request: URLRequest,
    completionHandler: @escaping (URLRequest?) -> Void) {
    completionHandler(nil)
  }
}
