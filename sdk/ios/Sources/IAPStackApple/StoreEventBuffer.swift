import Foundation

/// Holds store events that arrive before a React Native listener attaches.
///
/// The oldest event is dropped after `limit` so a session that never subscribes
/// cannot grow the queue without bound.
final class StoreEventBuffer<Element>: @unchecked Sendable {
  private let lock = NSLock()
  private var events: [Element] = []
  private let limit: Int

  init(limit: Int = 32) {
    precondition(limit > 0)
    self.limit = limit
  }

  func enqueue(_ event: Element) {
    lock.lock()
    events.append(event)
    if events.count > limit {
      events.removeFirst(events.count - limit)
    }
    lock.unlock()
  }

  func drain() -> [Element] {
    lock.lock()
    let copy = events
    events.removeAll()
    lock.unlock()
    return copy
  }

  func clear() {
    lock.lock()
    events.removeAll()
    lock.unlock()
  }
}
