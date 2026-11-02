import XCTest

@testable import IAPStackApple

final class DedupedPurchaseUpdateTests: XCTestCase {
  func testUnfinishedAndUpdatesDeliverEachTransactionOnce() async {
    let values = await collect(
      mergeDeduped(
        unfinished: { finite(["tx-1", "tx-2"]) },
        updates: { finite(["tx-1", "tx-3"]) },
        id: { $0 },
      ),
    )

    XCTAssertEqual(Set(values), Set(["tx-1", "tx-2", "tx-3"]))
    XCTAssertEqual(values.filter { $0 == "tx-1" }.count, 1)
  }

  func testALaterPassStillReadsUnfinishedWhenUpdatesDoesNotReplay() async {
    let values = await collect(
      mergeDeduped(
        unfinished: { finite(["tx-unfinished"]) },
        updates: { finite([]) },
        id: { $0 },
      ),
    )

    XCTAssertEqual(values, ["tx-unfinished"])
  }
}

private func finite(_ values: [String]) -> AsyncStream<String> {
  AsyncStream { continuation in
    for value in values {
      continuation.yield(value)
    }
    continuation.finish()
  }
}

private func collect(_ stream: AsyncStream<String>) async -> [String] {
  var values: [String] = []
  for await value in stream {
    values.append(value)
  }
  return values
}
