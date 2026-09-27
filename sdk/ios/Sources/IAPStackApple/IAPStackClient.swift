import Foundation

private let sdkVersion = "0.1.0-dev.1"

/// Provider-neutral HTTP client for the IAPStack v1 API.
public final class IAPStackClient {
  /// Creates a client, optionally using a caller-owned `URLSession`.
  ///
  /// When `session` is nil the client creates and owns an ephemeral session that
  /// `close()` invalidates. An injected session is never invalidated by the SDK.
  public init(config: IAPStackConfig, session: URLSession? = nil) throws {
    try config.validate()
    self.config = config
    if let session {
      self.session = session
      self.ownsSession = false
    } else {
      self.session = URLSession(configuration: .ephemeral)
      self.ownsSession = true
    }
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

  /// Invalidates the `URLSession` this client created; injected sessions are left untouched.
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
      try Task.checkCancellation()
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
    if (200 ... 299).contains(response.statusCode) {
      return try decodeJSONObject(from: data)
    }
    // Proxies and load balancers often answer 5xx with HTML or an empty body; keep
    // the status so retry and error classification still apply.
    let envelope = (try? decodeJSONObject(from: data)) ?? [:]
    throw parseApiError(statusCode: response.statusCode, response: response, body: envelope)
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

    let (bytes, response) = try await session.bytes(for: request)
    guard let httpResponse = response as? HTTPURLResponse else {
      bytes.task.cancel()
      throw IAPStackSDKError.protocolError(message: "non-HTTP response from server")
    }
    if httpResponse.expectedContentLength > Int64(config.maxResponseBytes) {
      bytes.task.cancel()
      throw IAPStackSDKError.protocolError(message: "IAPStack response exceeded maxResponseBytes")
    }
    var data = Data()
    for try await byte in bytes {
      data.append(byte)
      if data.count > config.maxResponseBytes {
        bytes.task.cancel()
        throw IAPStackSDKError.protocolError(message: "IAPStack response exceeded maxResponseBytes")
      }
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
    } catch {
      // Caller cancellation is not a timeout: surface it unchanged so it is never retried.
      if Task.isCancelled {
        throw CancellationError()
      }
      if case IAPStackSDKError.timeoutError = error {
        throw error
      }
      if error is CancellationError {
        throw IAPStackSDKError.timeoutError(message: "IAPStack request timed out", cause: error)
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
