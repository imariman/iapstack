import Foundation

/// Short-lived customer credentials minted by the app's authenticated trusted host.
public struct IAPStackCustomerSession: Equatable, Sendable, CustomStringConvertible, CustomDebugStringConvertible {
  public let baseURL: URL
  public let applicationId: String
  public let externalCustomerId: String
  public let customerToken: String
  public let expiresAt: Date

  public init(baseURL: URL, applicationId: String, externalCustomerId: String, customerToken: String, expiresAt: Date) {
    self.baseURL = baseURL
    self.applicationId = applicationId
    self.externalCustomerId = externalCustomerId
    self.customerToken = customerToken
    self.expiresAt = expiresAt
  }

  /// Client configuration with the SDK defaults for timeout, retries and response size.
  public var config: IAPStackConfig {
    IAPStackConfig(baseUri: baseURL, applicationId: applicationId, customerToken: customerToken)
  }

  /// Omits the bearer and customer identity so sessions never leak through logs.
  public var description: String {
    "IAPStackCustomerSession(applicationId: \(applicationId), expiresAt: \(expiresAt))"
  }

  public var debugDescription: String { description }
}

/// A trusted-host session request failed. `code` is stable and never contains response data.
public enum IAPStackSessionError: Error, Equatable, Sendable {
  /// The endpoint is not a plain HTTPS URL; nothing was sent.
  case invalidEndpoint
  /// The login token is empty or contains whitespace; nothing was sent.
  case invalidLoginToken
  /// The host answered with a status other than 200.
  case unavailable
  /// The response was oversized, malformed, or not a usable IAPStack configuration.
  case invalidResponse
  /// The host returned a session that had already expired.
  case expired
  case timeout
  case transportError

  public var code: String {
    switch self {
    case .invalidEndpoint, .invalidLoginToken: "invalid_session_request"
    case .unavailable: "session_unavailable"
    case .invalidResponse: "invalid_session_response"
    case .expired: "session_expired"
    case .timeout: "timeout"
    case .transportError: "transport_error"
    }
  }
}

/// Exchanges the app's own login token for an IAPStack customer session at a trusted host.
///
/// The host contract is `POST <endpoint>` with `Authorization: Bearer <login token>` and body
/// `{}`, answered by `base_url`, `application_id`, `external_customer_id`, `token` and
/// `expires_at`. The login bearer is sent only to the HTTPS endpoint: redirects, cookies and
/// caches are disabled, the response is capped at ``maxResponseBytes``, and the request at
/// 15 seconds without progress and 20 seconds overall. Never pass the durable application bearer.
public final class IAPStackSessionLoader {
  /// Largest accepted session response.
  public static let maxResponseBytes = 16_384

  private let session: URLSession
  private let ownsSession: Bool
  /// Wall clock used to reject expired sessions; only tests replace it.
  var clock: () -> Date = Date.init

  public init() {
    let configuration = URLSessionConfiguration.ephemeral
    configuration.httpShouldSetCookies = false
    configuration.httpCookieStorage = nil
    configuration.urlCache = nil
    configuration.timeoutIntervalForRequest = 15
    configuration.timeoutIntervalForResource = 20
    session = URLSession(configuration: configuration)
    ownsSession = true
  }

  /// Uses a caller-owned session, for tests; redirects are still rejected per task.
  init(session: URLSession) {
    self.session = session
    ownsSession = false
  }

  deinit {
    if ownsSession { session.invalidateAndCancel() }
  }

  /// Requests a session; throws ``IAPStackSessionError`` or `CancellationError`.
  public func load(endpoint: URL, loginToken: String) async throws -> IAPStackCustomerSession {
    guard endpoint.scheme?.lowercased() == "https", let host = endpoint.host, !host.isEmpty,
      endpoint.user == nil, endpoint.password == nil, endpoint.query == nil, endpoint.fragment == nil
    else { throw IAPStackSessionError.invalidEndpoint }
    guard !loginToken.isEmpty, !loginToken.contains(where: \.isWhitespace) else {
      throw IAPStackSessionError.invalidLoginToken
    }
    var request = URLRequest(url: endpoint, cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: 15)
    request.httpMethod = "POST"
    request.httpBody = Data("{}".utf8)
    request.setValue("Bearer \(loginToken)", forHTTPHeaderField: "Authorization")
    request.setValue("application/json", forHTTPHeaderField: "Content-Type")
    request.setValue("application/json", forHTTPHeaderField: "Accept")
    let body: Data
    do {
      body = try await fetch(request)
    } catch let error as IAPStackSessionError {
      throw error
    } catch {
      if Task.isCancelled || error is CancellationError { throw CancellationError() }
      if (error as? URLError)?.code == .timedOut { throw IAPStackSessionError.timeout }
      throw IAPStackSessionError.transportError
    }
    return try decode(body)
  }

  private func fetch(_ request: URLRequest) async throws -> Data {
    let (bytes, response) = try await session.bytes(for: request, delegate: RejectSessionRedirects())
    defer { bytes.task.cancel() }
    guard let http = response as? HTTPURLResponse, http.statusCode == 200 else {
      throw IAPStackSessionError.unavailable
    }
    guard response.expectedContentLength <= Self.maxResponseBytes else {
      throw IAPStackSessionError.invalidResponse
    }
    var data = Data()
    for try await byte in bytes {
      guard data.count < Self.maxResponseBytes else { throw IAPStackSessionError.invalidResponse }
      data.append(byte)
    }
    return data
  }

  private func decode(_ data: Data) throws -> IAPStackCustomerSession {
    guard let payload = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] else {
      throw IAPStackSessionError.invalidResponse
    }
    func field(_ key: String) throws -> String {
      guard let value = payload[key] as? String, !value.isEmpty else { throw IAPStackSessionError.invalidResponse }
      return value
    }
    guard let baseURL = URL(string: try field("base_url")),
      let expiresAt = sessionTimestamp(try field("expires_at"))
    else { throw IAPStackSessionError.invalidResponse }
    let session = IAPStackCustomerSession(baseURL: baseURL, applicationId: try field("application_id"),
      externalCustomerId: try field("external_customer_id"), customerToken: try field("token"),
      expiresAt: expiresAt)
    do {
      try validateExternalCustomerId(session.externalCustomerId)
      try session.config.validate()
    } catch {
      throw IAPStackSessionError.invalidResponse
    }
    guard expiresAt > clock() else { throw IAPStackSessionError.expired }
    return session
  }
}

/// Never replays the login bearer at a redirect target, including on injected sessions.
private final class RejectSessionRedirects: NSObject, URLSessionTaskDelegate {
  func urlSession(_ session: URLSession, task: URLSessionTask,
                  willPerformHTTPRedirection response: HTTPURLResponse, newRequest request: URLRequest,
                  completionHandler: @escaping (URLRequest?) -> Void) {
    completionHandler(nil)
  }
}

private func sessionTimestamp(_ value: String) -> Date? {
  let formatter = ISO8601DateFormatter()
  formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
  if let date = formatter.date(from: value) { return date }
  formatter.formatOptions = [.withInternetDateTime]
  return formatter.date(from: value)
}
