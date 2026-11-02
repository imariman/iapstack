import Foundation

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
