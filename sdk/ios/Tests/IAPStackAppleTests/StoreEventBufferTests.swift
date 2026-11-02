import XCTest

@testable import IAPStackApple

final class StoreEventBufferTests: XCTestCase {
  func testDrainReturnsEventsInOrderAndLeavesTheBufferEmpty() {
    let buffer = StoreEventBuffer<String>()
    buffer.enqueue("verified")
    buffer.enqueue("pending")
    XCTAssertEqual(buffer.drain(), ["verified", "pending"])
    XCTAssertEqual(buffer.drain(), [])
  }

  func testEnqueueDropsTheOldestEventPastTheLimit() {
    let buffer = StoreEventBuffer<Int>(limit: 2)
    buffer.enqueue(1)
    buffer.enqueue(2)
    buffer.enqueue(3)
    XCTAssertEqual(buffer.drain(), [2, 3])
  }

  func testClearDropsEventsThatBelongToADisposedSession() {
    let buffer = StoreEventBuffer<Int>()
    buffer.enqueue(1)
    buffer.clear()
    XCTAssertEqual(buffer.drain(), [])
  }
}
