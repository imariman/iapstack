import Foundation

/// Identity of one signed StoreKit transaction.
///
/// A replay is the same id, `signedDate`, and `revocationDate`. Apple signs a
/// new payload for a refund, revocation, or other update, so `signedDate`
/// moves and `revocationDate` is set. The id alone stays the same.
struct TransactionReplayKey: Hashable, Sendable {
  var transactionId: String
  var signedDate: Date?
  var revocationDate: Date?
}

func transactionReplayKey(
  transactionId: String,
  signedDate: Date?,
  revocationDate: Date?,
) -> TransactionReplayKey {
  TransactionReplayKey(
    transactionId: transactionId,
    signedDate: signedDate,
    revocationDate: revocationDate,
  )
}

/// Yields `unfinished`, then `updates`.
///
/// An `updates` element is dropped only when its key equals one read from
/// `unfinished` in this pass. That match is forgotten, so a later element with
/// a different signed version is delivered. On a later pass StoreKit does not
/// replay unfinished rows on `updates`; a refund of that id has a new
/// `signedDate` and `revocationDate`, so it is not the replay key.
func mergeDeduped<Element: Sendable, ID: Hashable & Sendable>(
  unfinished: @escaping @Sendable () -> AsyncStream<Element>,
  updates: @escaping @Sendable () -> AsyncStream<Element>,
  id: @escaping @Sendable (Element) -> ID,
) -> AsyncStream<Element> {
  AsyncStream { continuation in
    let task = Task {
      defer { continuation.finish() }
      var replay = Set<ID>()
      for await element in unfinished() {
        if Task.isCancelled { return }
        replay.insert(id(element))
        continuation.yield(element)
      }
      guard !Task.isCancelled else { return }
      for await element in updates() {
        if Task.isCancelled { return }
        // Same signed version as an unfinished row: the process replay.
        // A refund or revocation is a new signature and is not removed here.
        if replay.remove(id(element)) != nil {
          continue
        }
        continuation.yield(element)
      }
    }
    continuation.onTermination = { _ in
      task.cancel()
    }
  }
}

/// Fans one upstream async sequence out to every subscriber.
///
/// The upstream is created when the first subscriber arrives and cancelled when
/// the last one leaves. A later subscriber starts a new upstream. Two readers
/// therefore share one pass instead of each replaying it.
final class SharedAsyncMulticast<Element: Sendable>: @unchecked Sendable {
  private let lock = NSLock()
  private let makeSource: @Sendable () -> AsyncStream<Element>
  private var continuations: [UUID: AsyncStream<Element>.Continuation] = [:]
  private var source: Task<Void, Never>?
  private var generation = 0

  /// Creates a hub. `makeSource` runs when a subscriber arrives and nobody is
  /// listening. It must not call `subscribe`.
  init(makeSource: @escaping @Sendable () -> AsyncStream<Element>) {
    self.makeSource = makeSource
  }

  deinit {
    let task: Task<Void, Never>?
    let leftover: [AsyncStream<Element>.Continuation]
    lock.lock()
    task = source
    leftover = Array(continuations.values)
    continuations.removeAll()
    source = nil
    generation += 1
    lock.unlock()
    task?.cancel()
    for continuation in leftover {
      continuation.finish()
    }
  }

  func subscribe() -> AsyncStream<Element> {
    AsyncStream { continuation in
      let id = UUID()
      lock.lock()
      continuations[id] = continuation
      if source == nil {
        startLocked()
      }
      lock.unlock()
      continuation.onTermination = { [weak self] _ in
        self?.remove(id)
      }
    }
  }

  private func startLocked() {
    let token = generation
    let stream = makeSource()
    source = Task { [weak self] in
      // Let subscribe release the lock before the first element is forwarded.
      await Task.yield()
      guard let self else { return }
      for await element in stream {
        if Task.isCancelled { break }
        self.broadcast(element, generation: token)
      }
      self.upstreamEnded(token)
    }
  }

  private func broadcast(_ element: Element, generation token: Int) {
    let current: [AsyncStream<Element>.Continuation]
    lock.lock()
    if token != generation {
      lock.unlock()
      return
    }
    current = Array(continuations.values)
    lock.unlock()
    for continuation in current {
      continuation.yield(element)
    }
  }

  private func upstreamEnded(_ token: Int) {
    let finished: [AsyncStream<Element>.Continuation]
    lock.lock()
    guard token == generation, source != nil else {
      lock.unlock()
      return
    }
    finished = Array(continuations.values)
    continuations.removeAll()
    source = nil
    generation += 1
    lock.unlock()
    for continuation in finished {
      continuation.finish()
    }
  }

  private func remove(_ id: UUID) {
    lock.lock()
    guard continuations.removeValue(forKey: id) != nil else {
      lock.unlock()
      return
    }
    if continuations.isEmpty {
      generation += 1
      source?.cancel()
      source = nil
    }
    lock.unlock()
  }
}
