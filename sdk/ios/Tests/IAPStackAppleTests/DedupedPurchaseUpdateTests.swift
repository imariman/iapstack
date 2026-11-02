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

  func testRevocationAfterDedupeReachesTheSubscriber() async {
    let values = await collect(
      mergeDeduped(
        unfinished: { finite([Delivery(id: "tx-1", kind: "unfinished")]) },
        updates: {
          finite([
            Delivery(id: "tx-1", kind: "replay"),
            Delivery(id: "tx-1", kind: "revoked"),
          ])
        },
        id: \.id,
      ),
    )

    XCTAssertEqual(
      values,
      [
        Delivery(id: "tx-1", kind: "unfinished"),
        Delivery(id: "tx-1", kind: "revoked"),
      ],
    )
  }

  func testSecondPassDeliversRevocationWhenUpdatesDoesNotReplay() async {
    let signed = Date(timeIntervalSince1970: 1_700_000_000)
    let revokedAt = Date(timeIntervalSince1970: 1_700_086_400)
    let original = transactionReplayKey(
      transactionId: "tx-1",
      signedDate: signed,
      revocationDate: nil,
    )
    let revoked = transactionReplayKey(
      transactionId: "tx-1",
      signedDate: revokedAt,
      revocationDate: revokedAt,
    )
    let values = await collect(
      mergeDeduped(
        unfinished: { finite([original]) },
        updates: { finite([revoked]) },
        id: { $0 },
      ),
    )

    XCTAssertEqual(values, [original, revoked])
  }
}

private struct Delivery: Equatable, Sendable {
  var id: String
  var kind: String
}

private func finite<Element: Sendable>(_ values: [Element]) -> AsyncStream<Element> {
  AsyncStream { continuation in
    for value in values {
      continuation.yield(value)
    }
    continuation.finish()
  }
}

private func collect<Element: Sendable>(_ stream: AsyncStream<Element>) async -> [Element] {
  var values: [Element] = []
  for await value in stream {
    values.append(value)
  }
  return values
}
