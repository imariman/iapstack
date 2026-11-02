package com.iapstack.core

import java.util.Collections
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.concurrent.thread
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class OrderedStoreEventsTest {
  @Test
  fun replayKeepsBufferedEventsAheadOfOneThatArrivesDuringDelivery() {
    val mailbox = OrderedStoreEvents<String>()
    val delivered = Collections.synchronizedList(mutableListOf<String>())
    val enteredDeliver = CountDownLatch(1)
    val finishDeliver = CountDownLatch(1)
    mailbox.emit("old", bridgeReady = false) { delivered.add(it) }

    val listener = thread(name = "listener") {
      mailbox.addListener(bridgeReady = true) { event ->
        enteredDeliver.countDown()
        assertTrue(finishDeliver.await(2, TimeUnit.SECONDS))
        delivered.add(event)
      }
    }
    assertTrue(enteredDeliver.await(2, TimeUnit.SECONDS))
    val emitterFinished = AtomicBoolean(false)
    val emitter = thread(name = "emitter") {
      mailbox.emit("new", bridgeReady = true) { delivered.add(it) }
      emitterFinished.set(true)
    }
    Thread.sleep(150)
    assertFalse(emitterFinished.get())
    finishDeliver.countDown()
    listener.join(2_000)
    emitter.join(2_000)

    assertEquals(listOf("old", "new"), delivered)
  }
}
