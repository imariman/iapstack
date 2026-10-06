import Combine
import Foundation
import IAPStackApple

/// Main-actor state shared by the SwiftUI screen and deterministic flow tests.
@MainActor
public final class PurchaseStore: ObservableObject {
  public static let catalog: [String: AppleProductKind] = [
    "premium_lifetime": .nonConsumable,
    "premium_monthly": .subscription,
  ]

  @Published public private(set) var products: [AppleProduct] = []
  @Published public private(set) var entitlements: [Entitlement] = []
  @Published public private(set) var status = "Connect to your trusted host to verify purchases."
  @Published public private(set) var isConnected = false
  @Published public private(set) var isBusy = false
  @Published public private(set) var canMakePayments = false
  @Published public private(set) var pendingCount = 0
  @Published public private(set) var expiresAt: Date?

  private let loader: CustomerSessionLoading
  private let platform: AppleIAPPlatform
  private let makeClient: (IAPStackConfig) throws -> IAPStackClient
  private var session: CustomerSession?
  private var client: IAPStackClient?
  private var stack: AppleIAPStack?
  private var listener: Task<Void, Never>?
  private var generation = UUID()
  private var entitlementRevision = 0
  private var pending: [String: ApplePurchase] = [:]
  private var verifying: Set<String> = []

  public init(
    loader: CustomerSessionLoading = TrustedHostSessionLoader(),
    platform: AppleIAPPlatform = AppleStoreKitPlatform(),
    makeClient: @escaping (IAPStackConfig) throws -> IAPStackClient = { try IAPStackClient(config: $0) }
  ) {
    self.loader = loader
    self.platform = platform
    self.makeClient = makeClient
  }

  deinit { listener?.cancel(); client?.close() }

  /// The host derives customer identity from its own authenticated login, not form input.
  public func connect(endpoint: String, loginToken: String) async {
    guard !isBusy else { return }
    disconnect()
    isBusy = true
    let current = generation
    defer { if current == generation { isBusy = false } }
    do {
      guard let url = URL(string: endpoint.trimmingCharacters(in: .whitespacesAndNewlines)) else {
        throw SessionError.invalidEndpoint
      }
      let customerSession = try await loader.load(endpoint: url, loginToken: loginToken)
      try Task.checkCancellation()
      guard current == generation else { return }
      try customerSession.validate()
      let newClient = try makeClient(customerSession.config)
      let newStack = try AppleIAPStack(client: newClient, productKinds: Self.catalog, platform: platform)
      client = newClient
      stack = newStack
      session = customerSession
      expiresAt = customerSession.expiresAt
      isConnected = true
      status = "Customer session ready."
      // Start before any catalog/network work so unfinished rows and renewals are observed.
      let updates = newStack.purchaseUpdates
      listener = Task { [weak self] in
        for await purchase in updates {
          guard !Task.isCancelled else { return }
          await self?.receive(purchase, generation: current)
        }
      }
      await loadEntitlements(using: newStack, customer: customerSession.externalCustomerID, generation: current)
    } catch {
      guard current == generation else { return }
      status = safeMessage(error)
    }
  }

  public func disconnect() {
    generation = UUID()
    entitlementRevision = 0
    listener?.cancel()
    listener = nil
    client?.close()
    client = nil
    stack = nil
    session = nil
    expiresAt = nil
    entitlements = []
    pending = [:]
    verifying = []
    pendingCount = 0
    isConnected = false
    isBusy = false
    status = "Connect to your trusted host to verify purchases."
  }

  /// Catalog access works without a customer credential, including local StoreKit testing.
  public func queryProducts() async {
    guard !isBusy else { return }
    isBusy = true
    defer { isBusy = false }
    do {
      canMakePayments = try await platform.isAvailable()
      let query = try await platform.queryProducts(productIds: Set(Self.catalog.keys))
      guard query.products.allSatisfy({ Self.catalog[$0.id] == $0.kind }) else {
        throw AppleIAPStackError(code: "product_kind_mismatch", message: "Catalog mismatch")
      }
      products = query.products.sorted { $0.id < $1.id }
      status = query.notFoundProductIds.isEmpty
        ? "Catalog loaded."
        : "Some products are unavailable. Check your StoreKit configuration or App Store Connect."
    } catch { status = safeMessage(error) }
  }

  public func purchase(_ product: AppleProduct) async {
    guard !isBusy, let (stack, session) = readySession() else { return }
    isBusy = true
    let current = generation
    defer { if current == generation { isBusy = false } }
    do {
      guard let purchase = try await stack.launchPurchase(
        externalCustomerId: session.externalCustomerID, product: product
      ) else {
        if current == generation { status = "Awaiting approval. The update listener will verify it when approved." }
        return
      }
      await receive(purchase, generation: current)
    } catch {
      if current == generation { status = safeMessage(error) }
    }
  }

  public func retryPending() async {
    guard !isBusy, readySession() != nil else { return }
    isBusy = true
    let current = generation
    defer { if current == generation { isBusy = false } }
    for purchase in Array(pending.values) { await receive(purchase, generation: current) }
  }

  public func refreshEntitlements() async {
    guard !isBusy, let (stack, session) = readySession() else { return }
    isBusy = true
    let current = generation
    defer { if current == generation { isBusy = false } }
    await loadEntitlements(using: stack, customer: session.externalCustomerID, generation: current)
  }

  public func restore() async {
    guard !isBusy, let (stack, session) = readySession() else { return }
    isBusy = true
    let current = generation
    defer { if current == generation { isBusy = false } }
    do {
      _ = try await stack.restorePurchases(externalCustomerId: session.externalCustomerID)
      guard current == generation else { return }
      // Restore returns entitlement results, not transaction IDs. Retry remembered
      // failures idempotently so the local queue reflects what the server accepted.
      for purchase in Array(pending.values) { await receive(purchase, generation: current) }
      // A complete server snapshot is authoritative; an empty restore still refreshes access.
      await loadEntitlements(using: stack, customer: session.externalCustomerID, generation: current)
    } catch {
      if current == generation { status = safeMessage(error) + " Restore can be retried; unverified transactions remain unfinished." }
    }
  }

  private func receive(_ purchase: ApplePurchase, generation current: UUID) async {
    guard current == generation else { return }
    guard purchase.canVerify else {
      status = "StoreKit has not verified this transaction. It remains unfinished."
      return
    }
    // Never submit a transaction belonging to a different signed-in customer.
    guard purchase.appAccountToken?.lowercased() == session?.externalCustomerID,
      Self.catalog[purchase.productId] != nil else {
      status = "A transaction belongs to another customer or catalog; it remains unfinished."
      return
    }
    guard !verifying.contains(purchase.transactionId) else { return }
    pending[purchase.transactionId] = purchase
    pendingCount = pending.count
    guard let (stack, session) = readySession() else { return }
    verifying.insert(purchase.transactionId)
    defer { if current == generation { verifying.remove(purchase.transactionId) } }
    do {
      let result = try await stack.verifyPurchase(externalCustomerId: session.externalCustomerID, purchase: purchase)
      guard current == generation else { return }
      pending.removeValue(forKey: purchase.transactionId)
      pendingCount = pending.count
      entitlementRevision += 1
      entitlements = result.entitlements
      status = "Purchase verified and finished."
    } catch {
      if current == generation { status = safeMessage(error) + " Transaction remains unfinished. Use Retry verification." }
    }
  }

  private func readySession() -> (AppleIAPStack, CustomerSession)? {
    guard let stack, let session else { status = "Connect to your trusted host first."; return nil }
    guard session.expiresAt > Date() else {
      status = "Customer session expired. Reconnect to your host; unfinished purchases will be replayed."
      return nil
    }
    return (stack, session)
  }

  private func loadEntitlements(using stack: AppleIAPStack, customer: String, generation current: UUID) async {
    let revision = entitlementRevision
    do {
      let snapshot = try await stack.getEntitlements(customer)
      // A GET started before a transaction verified can return an older projection.
      guard current == generation, entitlementRevision == revision else { return }
      entitlements = snapshot.entitlements
      status = pending.isEmpty ? "Entitlements refreshed." : "Entitlements refreshed. Some transactions still await verification."
    } catch {
      if current == generation { status = safeMessage(error) }
    }
  }

  /// No raw server messages, customer IDs, bearer tokens or JWS evidence reach the UI.
  private func safeMessage(_ error: Error) -> String {
    if error is CancellationError { return "Operation cancelled." }
    if let apple = error as? AppleIAPStackError, apple.userCancelled || apple.code == "purchase_cancelled" {
      return "Purchase cancelled."
    }
    if case SessionError.expired = error { return "Customer session expired. Connect again." }
    if case SessionError.invalidEndpoint = error { return "Enter an HTTPS trusted-host session URL without credentials or a query." }
    if case IAPStackSDKError.apiError(let status, _, _, _, _, _) = error, status == 401 || status == 403 {
      return "Session rejected. Reconnect to your trusted host."
    }
    return "Request failed. Check your connection and host/store configuration, then retry."
  }
}
