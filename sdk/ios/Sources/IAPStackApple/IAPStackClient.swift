import Foundation

private let sdkVersion = "0.1.0-dev.1"

/// Provider-neutral HTTP client for the IAPStack v1 API.
public final class IAPStackClient {
  /// Creates a client with optional custom URLSession for testing.
  public init(config: IAPStackConfig, session: URLSession = .shared) throws {
    try config.validate()
    self.config = config
    self.session = session
    self.ownsSession = session !== URLSession.shared
  }

  private let config: IAPStackConfig
  private let session: URLSession
  private let ownsSession: Bool

  /// Verifies one signed purchase evidence row.
  public func verifyPurchase(_ purchase: PurchaseSubmission, requestId: String? = nil) async throws -> VerificationResult {
    let body = purchase.toDictionary()
    let json = try await request(
      method: "POST",
      path: ["v1", "applications", config.applicationId, "purchases:verify"],
      body: body,
      requestId: requestId,
    )
    return try VerificationResult.from(json)
  }

  /// Verifies a bounded batch of signed purchases.
  public func restorePurchases(
    _ purchases: [PurchaseSubmission],
    requestId: String? = nil,
  ) async throws -> RestoreResult {
    if purchases.isEmpty || purchases.count > 100 {
      throw IAPStackSDKError.configurationError(
        message: "purchases must contain between 1 and 100 items",
      )
    }
    let json = try await request(
      method: "POST",
      path: ["v1", "applications", config.applicationId, "purchases:restore"],
      body: ["purchases": purchases.map { $0.toDictionary() }],
      requestId: requestId,
    )
    return try RestoreResult.from(json)
  }

  /// Loads the current entitlement snapshot for one external customer.
  public func getEntitlements(_ externalCustomerId: String, requestId: String? = nil) async throws
    -> EntitlementSnapshot {
    if externalCustomerId.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
      throw IAPStackSDKError.configurationError(
        message: "externalCustomerId must not be empty",
      )
    }
    let json = try await request(
      method: "GET",
      path: [
        "v1",
        "applications",
        config.applicationId,
        "customers",
        externalCustomerId,
        "entitlements",
      ],
      body: nil,
      requestId: requestId,
    )
    return try EntitlementSnapshot.from(json)
  }

  /// Releases the owned `URLSession`.
  public func close() {
    if ownsSession {
      session.invalidateAndCancel()
    }
  }

  private func request(
    method: String,
    path: [String],
    body: [String: Any]?,
    requestId: String?,
  ) async throws -> [String: Any] {
    let normalizedRequestId = requestId?.trimmingCharacters(in: .whitespacesAndNewlines)
    if let normalized = normalizedRequestId, normalized.isEmpty {
      throw IAPStackSDKError.configurationError(message: "requestId cannot be empty")
    }

    var attempt = 1
    while attempt <= config.retryPolicy.maxAttempts {
      do {
        return try await requestAttempt(
          method: method,
          path: path,
          body: body,
          requestId: normalizedRequestId,
        )
      } catch {
        if shouldRetry(error: error, attempt: attempt) {
          let delay = config.retryPolicy.delayAfter(
            attempt: attempt,
            randomValue: Double.random(in: 0 ... 1),
          )
          if delay > 0 {
            try await Task.sleep(nanoseconds: UInt64(delay * 1_000_000_000))
          }
          attempt += 1
          continue
        }
        throw error
      }
    }
    throw IAPStackSDKError.transportError(message: "IAPStack request exhausted retry policy")
  }

  private func requestAttempt(
    method: String,
    path: [String],
    body: [String: Any]?,
    requestId: String?,
  ) async throws -> [String: Any] {
    let (data, response) = try await withTimeout {
      try await self.send(method: method, path: path, body: body, requestId: requestId)
    }
    if data.count > config.maxResponseBytes {
      throw IAPStackSDKError.protocolError(message: "IAPStack response exceeded maxResponseBytes")
    }
    let json = try decodeJSONObject(from: data)
    if (200 ... 299).contains(response.statusCode) {
      return json
    }
    throw parseApiError(statusCode: response.statusCode, response: response, body: json)
  }

  private func send(
    method: String,
    path: [String],
    body: [String: Any]?,
    requestId: String?,
  ) async throws -> (Data, HTTPURLResponse) {
    let url = append(path: path)
    var request = URLRequest(url: url)
    request.httpMethod = method
    request.setValue("application/json", forHTTPHeaderField: "Accept")
    request.setValue("Bearer \(config.customerToken)", forHTTPHeaderField: "Authorization")
    request.setValue("ios-swift/\(sdkVersion)", forHTTPHeaderField: "X-IAPStack-SDK")
    if let requestId {
      request.setValue(requestId, forHTTPHeaderField: "X-Request-ID")
    }

    if let body {
      if body.isEmpty {
        throw IAPStackSDKError.configurationError(message: "request body must not be empty")
      }
      request.httpBody = try JSONSerialization.data(withJSONObject: body)
      request.setValue("application/json", forHTTPHeaderField: "Content-Type")
    }

    let (data, response) = try await session.data(for: request)
    guard let httpResponse = response as? HTTPURLResponse else {
      throw IAPStackSDKError.protocolError(message: "non-HTTP response from server")
    }
    return (data, httpResponse)
  }

  private func withTimeout<T>(_ operation: @escaping () async throws -> T) async throws -> T {
    let timeout = max(1, UInt64(config.timeout * 1_000_000_000))
    do {
      return try await withThrowingTaskGroup(of: TimeoutRace<T>.self) { group in
        group.addTask { .value(try await operation()) }
        group.addTask {
          try await Task.sleep(nanoseconds: timeout)
          return .timeout
        }
        defer { group.cancelAll() }
        switch try await group.next() {
        case .value(let value):
          return value
        case .timeout:
          throw IAPStackSDKError.timeoutError(message: "IAPStack request timed out")
        case nil:
          throw IAPStackSDKError.transportError(message: "IAPStack request did not execute")
        }
      }
    } catch is CancellationError {
      throw IAPStackSDKError.timeoutError(message: "IAPStack request timed out")
    } catch {
      if case IAPStackSDKError.timeoutError = error {
        throw error
      }
      if let urlError = error as? URLError,
        urlError.code == .timedOut || urlError.code == .cancelled
      {
        throw IAPStackSDKError.timeoutError(
          message: "IAPStack request timed out",
          cause: error,
        )
      }
      if let urlError = error as? URLError {
        throw IAPStackSDKError.transportError(
          message: "IAPStack request failed before a response was received",
          cause: urlError,
        )
      }
      throw error
    }
  }

  private func shouldRetry(error: Error, attempt: Int) -> Bool {
    guard attempt < config.retryPolicy.maxAttempts else {
      return false
    }
    if error is CancellationError {
      return false
    }
    guard let sdkError = error as? IAPStackSDKError else {
      return error is URLError
    }
    return sdkError.isRetryable
  }

  private func parseApiError(
    statusCode: Int,
    response: HTTPURLResponse,
    body: [String: Any],
  ) -> IAPStackSDKError {
    var code = "http_error"
    var message = "IAPStack returned an unsuccessful response"
    var requestId: String?
    if let errorObject = body["error"] as? [String: Any] {
      if let parsedCode = errorObject["code"] as? String, !parsedCode.isEmpty {
        code = parsedCode
      }
      if let parsedMessage = errorObject["message"] as? String, !parsedMessage.isEmpty {
        message = parsedMessage
      }
      requestId = errorObject["request_id"] as? String
    }
    let headerRequestId = response.allHeaderFields.first(
      where: { ($0.key as? String)?.lowercased() == "x-request-id" },
    )?.value as? String
    return IAPStackSDKError.apiError(
      statusCode: statusCode,
      code: code,
      message: message,
      requestId: requestId ?? headerRequestId,
      retryable: statusCode == 429 || statusCode >= 500,
    )
  }

  private func decodeJSONObject(from data: Data) throws -> [String: Any] {
    if data.isEmpty {
      throw IAPStackSDKError.protocolError(message: "empty response body")
    }
    let object: Any
    do {
      object = try JSONSerialization.jsonObject(with: data)
    } catch {
      throw IAPStackSDKError.protocolError(message: "IAPStack returned invalid JSON", cause: error)
    }
    guard let dictionary = object as? [String: Any] else {
      throw IAPStackSDKError.protocolError(message: "IAPStack response was not a JSON object")
    }
    return dictionary
  }

  private func append(path segments: [String]) -> URL {
    var components = URLComponents(url: config.baseUri, resolvingAgainstBaseURL: false)
      ?? URLComponents()
    var path = components.percentEncodedPath
    if path.isEmpty {
      path = "/"
    }
    if !path.hasSuffix("/") {
      path += "/"
    }
    var allowed = CharacterSet.urlPathAllowed
    allowed.remove(charactersIn: "/")
    let encoded = segments.map { segment in
      segment.addingPercentEncoding(withAllowedCharacters: allowed) ?? segment
    }
    path += encoded.joined(separator: "/")
    components.percentEncodedPath = path
    return components.url ?? config.baseUri
  }
}

private enum TimeoutRace<Value> {
  case value(Value)
  case timeout
}
