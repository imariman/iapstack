import Foundation

private let sdkVersion = "0.1.0-dev.1"

/// Provider-neutral HTTP client for the IAPStack v1 API.
public final class IAPStackClient {
  /// Creates a client, optionally using a caller-owned `URLSession`.
  ///
  /// When `session` is nil the client creates and owns an ephemeral session that
  /// `close()` invalidates. An injected session is never invalidated by the SDK.
  public convenience init(config: IAPStackConfig, session: URLSession? = nil) throws {
    try self.init(config: config, session: session, ownedSessionConfiguration: .ephemeral)
  }

  /// Uses `ownedSessionConfiguration` for the session created when `session` is nil.
  init(
    config: IAPStackConfig,
    session: URLSession?,
    ownedSessionConfiguration: URLSessionConfiguration,
  ) throws {
    try config.validate()
    self.config = config
    if let session {
      self.session = session
      self.ownsSession = false
    } else {
      self.session = URLSession(configuration: ownedSessionConfiguration)
      self.ownsSession = true
    }
  }

  private static let closedError = IAPStackSDKError.configurationError(
    message: "IAPStackClient is closed",
  )

  private let config: IAPStackConfig
  private let session: URLSession
  private let ownsSession: Bool
  /// Wall clock used to resolve HTTP-date `Retry-After` values; only tests replace it.
  var clock: () -> Date = Date.init
  /// Suspends between attempts; only tests replace it.
  var sleep: (TimeInterval) async throws -> Void = { seconds in
    try await Task.sleep(nanoseconds: UInt64(seconds * 1_000_000_000))
  }
  private let lifecycleLock = NSLock()
  private var closed = false

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
    try validateExternalCustomerId(externalCustomerId)
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

  /// Closes the client and invalidates the `URLSession` it created; injected sessions are
  /// left untouched.
  ///
  /// Later calls, and retries of calls already in flight, throw a non-retryable
  /// `IAPStackSDKError.configurationError` without touching the session.
  public func close() {
    lifecycleLock.lock()
    defer { lifecycleLock.unlock() }
    guard !closed else {
      return
    }
    closed = true
    if ownsSession {
      session.invalidateAndCancel()
    }
  }

  private var isClosed: Bool {
    lifecycleLock.lock()
    defer { lifecycleLock.unlock() }
    return closed
  }

  private func request(
    method: String,
    path: [String],
    body: [String: Any]?,
    requestId: String?,
  ) async throws -> [String: Any] {
    // Blank request IDs are dropped, matching the other IAPStack SDKs.
    let normalizedRequestId = requestId?
      .trimmingCharacters(in: .whitespacesAndNewlines)
      .nilIfEmpty

    var attempt = 1
    while attempt <= config.retryPolicy.maxAttempts {
      try Task.checkCancellation()
      if isClosed {
        throw Self.closedError
      }
      do {
        return try await requestAttempt(
          method: method,
          path: path,
          body: body,
          requestId: normalizedRequestId,
        )
      } catch {
        if shouldRetry(error: error, attempt: attempt) {
          var retryAfter: TimeInterval?
          if case let IAPStackSDKError.apiError(_, _, _, _, _, cooldown) = error {
            retryAfter = cooldown
          }
          let delay = config.retryPolicy.delayAfter(
            attempt: attempt,
            randomValue: Double.random(in: 0 ... 1),
            retryAfter: retryAfter,
          )
          if delay > 0 {
            try await sleep(delay)
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

    let (data, response) = try await load(request)
    guard let httpResponse = response as? HTTPURLResponse else {
      throw IAPStackSDKError.protocolError(message: "non-HTTP response from server")
    }
    return (data, httpResponse)
  }

  /// Runs one data task and collects the body in the chunks URLSession delivers,
  /// cancelling the task as soon as it would exceed `maxResponseBytes`.
  private func load(_ request: URLRequest) async throws -> (Data, URLResponse) {
    let collector = BoundedBodyCollector(limit: config.maxResponseBytes)
    let task = try makeTask(for: request, delegate: collector)
    return try await withTaskCancellationHandler {
      try await withCheckedThrowingContinuation { continuation in
        collector.start(task, continuation: continuation)
      }
    } onCancel: {
      collector.cancel(task)
    }
  }

  /// Creates the task under the lifecycle lock. A task created on a session that
  /// `close()` has invalidated raises an Objective-C exception, not a Swift error.
  private func makeTask(
    for request: URLRequest,
    delegate: URLSessionDataDelegate,
  ) throws -> URLSessionDataTask {
    lifecycleLock.lock()
    defer { lifecycleLock.unlock() }
    if closed {
      throw Self.closedError
    }
    let task = session.dataTask(with: request)
    task.delegate = delegate
    return task
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
      // Anything else that cancelled the task (close(), the owner of an injected session
      // invalidating it, a delegate rejecting a challenge) fails the same way on retry.
      if isCancellation(error) {
        if isClosed {
          throw Self.closedError
        }
        throw IAPStackSDKError.transportError(
          message: "IAPStack request was cancelled by its URLSession",
          cause: error,
        )
      }
      if error is IAPStackSDKError {
        throw error
      }
      if let urlError = error as? URLError, urlError.code == .timedOut {
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
    guard attempt < config.retryPolicy.maxAttempts, !isClosed else {
      return false
    }
    if isCancellation(error) {
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
    let headerRequestId = headerValue("x-request-id", in: response)
    return IAPStackSDKError.apiError(
      statusCode: statusCode,
      code: code,
      message: message,
      requestId: requestId ?? headerRequestId,
      retryable: statusCode == 429 || statusCode >= 500,
      retryAfter: parseRetryAfter(headerValue("retry-after", in: response), now: clock()),
    )
  }

  private func headerValue(_ name: String, in response: HTTPURLResponse) -> String? {
    response.allHeaderFields.first(
      where: { ($0.key as? String)?.lowercased() == name },
    )?.value as? String
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

/// Per-task delegate that buffers one response body and fails once it exceeds `limit`.
private final class BoundedBodyCollector: NSObject, URLSessionDataDelegate, @unchecked Sendable {
  init(limit: Int) {
    self.limit = limit
  }

  private let limit: Int
  private let lock = NSLock()
  private var continuation: CheckedContinuation<(Data, URLResponse), Error>?
  private var earlyOutcome: Result<(Data, URLResponse), Error>?
  private var finished = false
  private var response: URLResponse?
  private var body = Data()

  func start(_ task: URLSessionDataTask, continuation: CheckedContinuation<(Data, URLResponse), Error>) {
    lock.lock()
    if let earlyOutcome {
      // Cancelled before the task started.
      lock.unlock()
      continuation.resume(with: earlyOutcome)
      return
    }
    self.continuation = continuation
    lock.unlock()
    task.resume()
  }

  func cancel(_ task: URLSessionTask) {
    finish(.failure(CancellationError()))
    task.cancel()
  }

  func urlSession(
    _ session: URLSession,
    dataTask: URLSessionDataTask,
    didReceive response: URLResponse,
    completionHandler: @escaping (URLSession.ResponseDisposition) -> Void,
  ) {
    let declaredLength = response.expectedContentLength
    if declaredLength > Int64(limit) {
      finish(.failure(Self.tooLarge()))
      completionHandler(.cancel)
      return
    }
    lock.lock()
    self.response = response
    if declaredLength > 0 {
      body.reserveCapacity(Int(declaredLength))
    }
    lock.unlock()
    completionHandler(.allow)
  }

  func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
    lock.lock()
    if finished {
      lock.unlock()
      return
    }
    if data.count > limit - body.count {
      lock.unlock()
      finish(.failure(Self.tooLarge()))
      dataTask.cancel()
      return
    }
    body.append(data)
    lock.unlock()
  }

  func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
    if let error {
      finish(.failure(error))
      return
    }
    lock.lock()
    let response = self.response ?? task.response
    let body = self.body
    lock.unlock()
    guard let response else {
      finish(.failure(IAPStackSDKError.protocolError(message: "IAPStack response had no headers")))
      return
    }
    finish(.success((body, response)))
  }

  /// Delivers the first outcome; later ones (such as the cancellation that follows a
  /// size-limit failure) are dropped.
  private func finish(_ outcome: Result<(Data, URLResponse), Error>) {
    lock.lock()
    if finished {
      lock.unlock()
      return
    }
    finished = true
    body = Data()
    let continuation = self.continuation
    self.continuation = nil
    if continuation == nil {
      earlyOutcome = outcome
    }
    lock.unlock()
    continuation?.resume(with: outcome)
  }

  private static func tooLarge() -> IAPStackSDKError {
    .protocolError(message: "IAPStack response exceeded maxResponseBytes")
  }
}

private extension String {
  var nilIfEmpty: String? {
    isEmpty ? nil : self
  }
}
