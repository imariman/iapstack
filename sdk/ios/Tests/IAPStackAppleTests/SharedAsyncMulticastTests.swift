import XCTest

@testable import IAPStackApple

final class SharedAsyncMulticastTests: XCTestCase {
  func testConcurrentSubscribersShareOneUpstreamAndSeeEachValueOnce() async throws {
    let starts = UpstreamStarts()
    let sources = Continuations()
    let hub = SharedAsyncMulticast<Int> {
      AsyncStream { continuation in
        starts.increment()
        sources.add(continuation)
      }
    }

    let first = Task { await next(hub.subscribe()) }
    let second = Task { await next(hub.subscribe()) }
    try await waitUntil { starts.value == 1 && sources.count == 1 }

    sources.yield(7)
    let firstValue = await first.value
    let secondValue = await second.value
    XCTAssertEqual(firstValue, 7)
    XCTAssertEqual(secondValue, 7)
    XCTAssertEqual(starts.value, 1)

    try await waitUntil { starts.value == 1 }
    let third = Task { await next(hub.subscribe()) }
    try await waitUntil { starts.value == 2 && sources.count == 2 }
    sources.yield(8)
    let thirdValue = await third.value
    XCTAssertEqual(thirdValue, 8)
  }

  func testFinishedUpstreamEndsSubscribers() async throws {
    let sources = Continuations()
    let hub = SharedAsyncMulticast<Int> {
      AsyncStream { continuation in
        sources.add(continuation)
      }
    }
    let values = Task<[Int], Never> {
      var collected: [Int] = []
      for await value in hub.subscribe() {
        collected.append(value)
      }
      return collected
    }
    try await waitUntil { sources.count == 1 }
    sources.yield(1)
    sources.finish()
    let collected = await values.value
    XCTAssertEqual(collected, [1])
  }
}

private func next(_ stream: AsyncStream<Int>) async -> Int? {
  var iterator = stream.makeAsyncIterator()
  return await iterator.next()
}

private func waitUntil(
  timeoutNanoseconds: UInt64 = 2_000_000_000,
  _ condition: @Sendable () -> Bool,
) async throws {
  let steps = timeoutNanoseconds / 10_000_000
  for _ in 0..<steps {
    if condition() { return }
    try await Task.sleep(nanoseconds: 10_000_000)
  }
  if !condition() {
    throw WaitError.timedOut
  }
}

private enum WaitError: Error {
  case timedOut
}

private final class UpstreamStarts: @unchecked Sendable {
  private let lock = NSLock()
  private var n = 0
  func increment() { lock.withLock { n += 1 } }
  var value: Int { lock.withLock { n } }
}

private final class Continuations: @unchecked Sendable {
  private let lock = NSLock()
  private var items: [AsyncStream<Int>.Continuation] = []
  func add(_ continuation: AsyncStream<Int>.Continuation) {
    lock.withLock { items.append(continuation) }
  }
  var count: Int { lock.withLock { items.count } }
  func yield(_ value: Int) {
    let current = lock.withLock { items }
    for continuation in current {
      continuation.yield(value)
    }
  }
  func finish() {
    let current = lock.withLock { items }
    for continuation in current {
      continuation.finish()
    }
  }
}
