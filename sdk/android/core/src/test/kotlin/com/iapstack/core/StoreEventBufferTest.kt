package com.iapstack.core

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

class StoreEventBufferTest {
  @Test
  fun drainReturnsEventsInOrderAndLeavesTheBufferEmpty() {
    val buffer = StoreEventBuffer<String>()
    buffer.enqueue("verified")
    buffer.enqueue("pending")
    assertEquals(listOf("verified", "pending"), buffer.drain())
    assertEquals(emptyList(), buffer.drain())
  }

  @Test
  fun enqueueDropsTheOldestEventPastTheLimit() {
    val buffer = StoreEventBuffer<Int>(limit = 2)
    buffer.enqueue(1)
    buffer.enqueue(2)
    buffer.enqueue(3)
    assertEquals(listOf(2, 3), buffer.drain())
  }

  @Test
  fun clearDropsEventsThatBelongToADisposedSession() {
    val buffer = StoreEventBuffer<Int>()
    buffer.enqueue(1)
    buffer.clear()
    assertEquals(emptyList(), buffer.drain())
    assertFailsWith<IllegalArgumentException> { StoreEventBuffer<Int>(limit = 0) }
  }
}
