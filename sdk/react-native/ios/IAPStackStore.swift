import Foundation
import React

/// Thin StoreKit companion bridge. Evidence and transaction finishing stay in AppleIAPStack.
@objc(IAPStackStore)
final class IAPStackStore: RCTEventEmitter {
  private final class Session {
    let customer: String
    let companion: AppleIAPStack
    let client: IAPStackClient
    var products: [String: AppleProduct] = [:]
    var tasks: [UUID: Task<Void, Never>] = [:]
    var updates: Task<Void, Never>?
    init(customer: String, companion: AppleIAPStack, client: IAPStackClient) {
      self.customer = customer
      self.companion = companion
      self.client = client
    }
    func cancel() {
      updates?.cancel()
      client.close()
      tasks.values.forEach { $0.cancel() }
      tasks.removeAll()
      products.removeAll()
    }
  }
  private var session: Session?
  private var listening = false
  override static func requiresMainQueueSetup() -> Bool { true }
  override var methodQueue: DispatchQueue! { DispatchQueue.main }
  override func supportedEvents() -> [String]! { ["IAPStackStoreUpdate"] }
  override func startObserving() { listening = true }
  override func stopObserving() { listening = false }

  /// Exchanges a separate host login for a short-lived customer session without exposing it to redirects.
  @objc(requestSession:loginToken:resolve:reject:)
  func requestSession(_ endpoint: String, loginToken: String,
                      resolve: @escaping RCTPromiseResolveBlock, reject: @escaping RCTPromiseRejectBlock) {
    Task { @MainActor in
      do { resolve(try await loadCustomerSession(endpoint: endpoint, loginToken: loginToken)) }
      catch {
        let code: String
        if let failure = error as? SessionBootstrapError { code = failure.code }
        else if (error as? URLError)?.code == .timedOut { code = "timeout" }
        else { code = "transport_error" }
        reject(code, "Customer session request failed (\(code))", nil)
      }
    }
  }

  @objc(httpRequest:operation:customer:payload:requestId:resolve:reject:)
  func httpRequest(_ config: NSDictionary, operation: String, customer: String, payload: NSDictionary,
                   requestId: String?, resolve: @escaping RCTPromiseResolveBlock, reject: @escaping RCTPromiseRejectBlock) {
    Task { @MainActor in
      do {
        guard let base = config["baseUri"] as? String, let url = URL(string: base),
          let application = config["applicationId"] as? String, let token = config["customerToken"] as? String,
          let retry = config["retryPolicy"] as? [String: Any],
          let attempts = retry["maxAttempts"] as? Int, let baseDelay = retry["baseDelayMs"] as? Double,
          let maxDelay = retry["maxDelayMs"] as? Double, let timeout = config["timeoutMs"] as? Double,
          let limit = config["maxResponseBytes"] as? Int
        else { throw bridgeError("invalid_configuration") }
        let client = try IAPStackClient(config: IAPStackConfig(baseUri: url, applicationId: application,
          customerToken: token, timeout: timeout / 1000,
          retryPolicy: IAPStackRetryPolicy(maxAttempts: attempts, baseDelay: baseDelay / 1000, maxDelay: maxDelay / 1000),
          maxResponseBytes: limit, allowInsecureHttp: config["allowInsecureHttp"] as? Bool ?? false))
        defer { client.close() }
        switch operation {
        case "purchases:verify":
          resolve(verification(try await client.verifyPurchase(submission(payload), requestId: requestId)))
        case "purchases:restore":
          guard let rows = payload["purchases"] as? [NSDictionary] else { throw bridgeError("invalid_submission") }
          let result = try await client.restorePurchases(rows.map(submission), requestId: requestId)
          resolve(["results": result.results.map(verification)])
        case "entitlements":
          let result = try await client.getEntitlements(customer, requestId: requestId)
          resolve(["customer_id": result.customerId, "entitlements": result.entitlements.map(entitlement)])
        default: throw bridgeError("unsupported_operation")
        }
      } catch { rejectError(reject, error) }
    }
  }

  @objc(configure:resolve:reject:)
  func configure(_ config: NSDictionary, resolve: @escaping RCTPromiseResolveBlock, reject: @escaping RCTPromiseRejectBlock) {
    do {
      guard session == nil else { throw bridgeError("session_exists") }
      guard config["storefront"] as? String == "apple" else { throw bridgeError("unsupported_platform") }
      guard let base = config["baseUri"] as? String, let url = URL(string: base),
        let application = config["applicationId"] as? String,
        let token = config["customerToken"] as? String,
        let customer = config["externalCustomerId"] as? String,
        let uuid = UUID(uuidString: customer), uuid.uuidString.lowercased() == customer,
        let catalog = config["productKinds"] as? [String: String], !catalog.isEmpty
      else { throw bridgeError("invalid_configuration") }
      var kinds: [String: AppleProductKind] = [:]
      for (id, value) in catalog {
        guard !id.isEmpty, id.trimmingCharacters(in: .whitespacesAndNewlines) == id,
          let kind = AppleProductKind(rawValue: value)
        else { throw bridgeError("invalid_product_catalog") }
        kinds[id] = kind
      }
      let client = try IAPStackClient(config: IAPStackConfig(baseUri: url,
        applicationId: application, customerToken: token,
        allowInsecureHttp: config["allowInsecureHttp"] as? Bool ?? false))
      let companion = try AppleIAPStack(client: client, productKinds: kinds)
      let next = Session(customer: customer, companion: companion, client: client)
      session = next
      next.updates = Task { @MainActor [weak self, weak next] in
        guard let next else { return }
        for await purchase in companion.purchaseUpdates {
          if Task.isCancelled { return }
          do {
            let result = try await companion.verifyPurchase(externalCustomerId: next.customer, purchase: purchase)
            guard !Task.isCancelled else { return }
            self?.emit(next, ["type": "verified", "result": verification(result)])
          } catch {
            if Task.isCancelled { return }
            self?.emit(next, errorEvent(error))
          }
        }
      }
      resolve(nil)
    } catch { rejectError(reject, error) }
  }

  @objc(isAvailable:reject:)
  func isAvailable(_ resolve: @escaping RCTPromiseResolveBlock, reject: @escaping RCTPromiseRejectBlock) {
    operation(resolve, reject) { try await $0.companion.isAvailable() }
  }
  @objc(resolveHuaweiEnvironment:reject:)
  func resolveHuaweiEnvironment(_ resolve: @escaping RCTPromiseResolveBlock, reject: @escaping RCTPromiseRejectBlock) {
    reject("unsupported_platform", "Huawei is Android-only", nil)
  }
  @objc(queryProducts:resolve:reject:)
  func queryProducts(_ ids: [String], resolve: @escaping RCTPromiseResolveBlock, reject: @escaping RCTPromiseRejectBlock) {
    operation(resolve, reject) { current in
      let wanted = Set(ids)
      guard !wanted.isEmpty else { throw bridgeError("invalid_product_ids") }
      for id in wanted { current.products.removeValue(forKey: id) }
      let query = try await current.companion.queryProducts(wanted)
      for product in query.products { current.products[product.id] = product }
      return ["products": query.products.map { p in
        ["id": p.id, "selectionKey": p.id, "kind": p.kind.rawValue,
         "title": p.title, "description": p.description, "price": p.price, "currencyCode": p.currencyCode]
      }, "notFoundProductIds": Array(query.notFoundProductIds)] as [String: Any]
    }
  }
  @objc(purchase:requestId:resolve:reject:)
  func purchase(_ selectionKey: String, requestId: String?, resolve: @escaping RCTPromiseResolveBlock, reject: @escaping RCTPromiseRejectBlock) {
    operation(resolve, reject) { current in
      guard let product = current.products[selectionKey] else { throw bridgeError("product_not_queried") }
      guard let purchase = try await current.companion.launchPurchase(externalCustomerId: current.customer, product: product)
      else { return NSNull() }
      return verification(try await current.companion.verifyPurchase(externalCustomerId: current.customer,
        purchase: purchase, requestId: requestId))
    }
  }
  @objc(restore:resolve:reject:)
  func restore(_ requestId: String?, resolve: @escaping RCTPromiseResolveBlock, reject: @escaping RCTPromiseRejectBlock) {
    operation(resolve, reject) { current in
      let result = try await current.companion.restorePurchases(externalCustomerId: current.customer, requestId: requestId)
      return ["results": result.results.map(verification)]
    }
  }
  @objc(getEntitlements:resolve:reject:)
  func getEntitlements(_ requestId: String?, resolve: @escaping RCTPromiseResolveBlock, reject: @escaping RCTPromiseRejectBlock) {
    operation(resolve, reject) { current in
      let result = try await current.companion.getEntitlements(current.customer, requestId: requestId)
      return ["customer_id": result.customerId, "entitlements": result.entitlements.map(entitlement)]
    }
  }
  @objc(dispose:reject:)
  func dispose(_ resolve: @escaping RCTPromiseResolveBlock, reject: @escaping RCTPromiseRejectBlock) {
    clearSession()
    resolve(nil)
  }
  override func invalidate() {
    // Never skip cleanup: a weak deferred hop leaves the old listener and bearer running.
    if Thread.isMainThread { clearSession() } else { DispatchQueue.main.async { self.clearSession() } }
    super.invalidate()
  }
  private func clearSession() {
    let old = session
    session = nil
    old?.cancel()
  }
  private func operation(_ resolve: @escaping RCTPromiseResolveBlock, _ reject: @escaping RCTPromiseRejectBlock,
                         action: @escaping (Session) async throws -> Any) {
    guard let current = session else { reject("not_configured", "Configure a customer session first", nil); return }
    let id = UUID()
    let task = Task { @MainActor [weak self] in
      defer { current.tasks.removeValue(forKey: id) }
      do {
        let result = try await action(current)
        try Task.checkCancellation()
        guard self?.session === current else { throw bridgeError("session_disposed") }
        resolve(result)
      } catch {
        if Task.isCancelled { reject("session_disposed", "Customer session was disposed", nil) }
        else { rejectError(reject, error) }
      }
    }
    current.tasks[id] = task
  }
  private func emit(_ current: Session, _ body: [String: Any]) {
    if session === current && listening { sendEvent(withName: "IAPStackStoreUpdate", body: body) }
  }
}

private struct SessionBootstrapError: Error {
  let code: String
}

private final class RejectSessionRedirects: NSObject, URLSessionTaskDelegate {
  func urlSession(_ session: URLSession, task: URLSessionTask,
                  willPerformHTTPRedirection response: HTTPURLResponse, newRequest request: URLRequest,
                  completionHandler: @escaping (URLRequest?) -> Void) {
    completionHandler(nil)
  }
}

private func loadCustomerSession(endpoint: String, loginToken: String) async throws -> [String: String] {
  guard let url = URL(string: endpoint), url.scheme?.lowercased() == "https",
    let host = url.host, !host.isEmpty, url.user == nil, url.password == nil,
    url.query == nil, url.fragment == nil,
    !loginToken.isEmpty, !loginToken.contains(where: { $0.isWhitespace })
  else { throw SessionBootstrapError(code: "invalid_session_request") }
  let configuration = URLSessionConfiguration.ephemeral
  configuration.httpShouldSetCookies = false
  configuration.urlCache = nil
  configuration.timeoutIntervalForRequest = 15
  configuration.timeoutIntervalForResource = 15
  let session = URLSession(configuration: configuration)
  defer { session.invalidateAndCancel() }
  var request = URLRequest(url: url, cachePolicy: .reloadIgnoringLocalCacheData)
  request.httpMethod = "POST"
  request.httpBody = Data("{}".utf8)
  request.setValue("Bearer \(loginToken)", forHTTPHeaderField: "Authorization")
  request.setValue("application/json", forHTTPHeaderField: "Content-Type")
  request.setValue("application/json", forHTTPHeaderField: "Accept")
  let (bytes, response) = try await session.bytes(for: request, delegate: RejectSessionRedirects())
  defer { bytes.task.cancel() }
  guard let http = response as? HTTPURLResponse, http.statusCode == 200 else {
    throw SessionBootstrapError(code: "session_unavailable")
  }
  let limit = 16_384
  guard response.expectedContentLength <= limit else {
    throw SessionBootstrapError(code: "invalid_session_response")
  }
  var data = Data()
  for try await byte in bytes {
    guard data.count < limit else { throw SessionBootstrapError(code: "invalid_session_response") }
    data.append(byte)
  }
  guard let payload = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any],
    let base = payload["base_url"] as? String, let baseURL = URL(string: base),
    let application = payload["application_id"] as? String,
    let customer = payload["external_customer_id"] as? String,
    !customer.isEmpty, customer.trimmingCharacters(in: .whitespacesAndNewlines) == customer,
    let token = payload["token"] as? String, let expires = payload["expires_at"] as? String
  else { throw SessionBootstrapError(code: "invalid_session_response") }
  do {
    try IAPStackConfig(baseUri: baseURL, applicationId: application, customerToken: token).validate()
  } catch { throw SessionBootstrapError(code: "invalid_session_response") }
  let formatter = ISO8601DateFormatter()
  formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
  var expiry = formatter.date(from: expires)
  if expiry == nil {
    formatter.formatOptions = [.withInternetDateTime]
    expiry = formatter.date(from: expires)
  }
  guard let expiry else { throw SessionBootstrapError(code: "invalid_session_response") }
  guard expiry > Date() else { throw SessionBootstrapError(code: "session_expired") }
  return ["base_url": base, "application_id": application, "external_customer_id": customer,
          "token": token, "expires_at": expires]
}

private func bridgeError(_ code: String) -> AppleIAPStackError {
  AppleIAPStackError(code: code, message: "IAPStack operation failed (\(code))")
}
private func errorEvent(_ error: Error) -> [String: Any] {
  let code: String
  if let apple = error as? AppleIAPStackError { code = apple.code }
  else if let sdk = error as? IAPStackSDKError {
    switch sdk {
    case let .apiError(_, value, _, _, _, _): code = value
    case .transportError: code = "transport_error"
    case .timeoutError: code = "timeout"
    case .protocolError: code = "protocol_error"
    case .configurationError: code = "invalid_configuration"
    }
  } else { code = "native_error" }
  return ["type": "error", "code": code, "message": "IAPStack operation failed (\(code))"]
}
private func rejectError(_ reject: RCTPromiseRejectBlock, _ error: Error) {
  let event = errorEvent(error)
  var info: [String: Any] = [:]
  if case let IAPStackSDKError.apiError(status, _, _, requestId, retryable, retryAfter) = error {
    info = ["statusCode": status, "retryable": retryable]
    if let requestId { info["requestId"] = requestId }
    if let retryAfter { info["retryAfterMs"] = retryAfter * 1000 }
  }
  reject(event["code"] as? String, event["message"] as? String,
    NSError(domain: "IAPStack", code: 1, userInfo: info))
}
private func timestamp(_ date: Date?) -> Any {
  guard let date else { return NSNull() }
  // Keep sub-second precision so access windows never open early in JS.
  let formatter = ISO8601DateFormatter()
  formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
  return formatter.string(from: date)
}
private func entitlement(_ value: Entitlement) -> [String: Any] {
  ["key": value.key, "access": value.access, "reason": value.reason, "version": value.version,
   "effective_starts_at": timestamp(value.effectiveStartsAt), "effective_ends_at": timestamp(value.effectiveEndsAt)]
}
private func verification(_ result: VerificationResult) -> [String: Any] {
  ["verified_at": timestamp(result.verifiedAt), "customer_id": result.customerId,
   "entitlements": result.entitlements.map(entitlement)]
}

private func submission(_ value: NSDictionary) throws -> PurchaseSubmission {
  guard let customer = value["external_customer_id"] as? String,
    let products = value["claimed_products"] as? [String],
    let evidence = value["evidence"] as? [String: Any]
  else { throw bridgeError("invalid_submission") }
  let bindings = try (value["customer_bindings"] as? [[String: Any]] ?? []).map { binding in
    guard let kind = binding["kind"] as? String, let value = binding["value"] as? String
    else { throw bridgeError("invalid_submission") }
    return CustomerBinding(kind: kind, value: value)
  }
  return try PurchaseSubmission(externalCustomerId: customer, claimedProducts: products,
    evidence: evidence, customerBindings: bindings)
}
