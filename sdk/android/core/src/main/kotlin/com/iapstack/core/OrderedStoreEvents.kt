package com.iapstack.core

/**
 * Keeps store events until a listener attaches, and delivers them in order.
 *
 * Delivery runs while the mailbox lock is held, so an event that arrives during
 * a replay cannot jump ahead of the events already buffered.
 */
internal class OrderedStoreEvents<T>(limit: Int = 32) {
  private val gate = Any()
  private val buffer = StoreEventBuffer<T>(limit)
  private var listeners = 0

  fun addListener(bridgeReady: Boolean, deliver: (T) -> Unit) {
    synchronized(gate) {
      listeners++
      if (listeners == 1 && bridgeReady) {
        buffer.drain().forEach(deliver)
      }
    }
  }

  fun removeListeners(count: Int) {
    synchronized(gate) {
      listeners = maxOf(0, listeners - count)
    }
  }

  fun emit(event: T, bridgeReady: Boolean, deliver: (T) -> Unit) {
    synchronized(gate) {
      if (listeners == 0 || !bridgeReady) {
        buffer.enqueue(event)
      } else {
        buffer.drain().forEach(deliver)
        deliver(event)
      }
    }
  }

  fun clear() {
    synchronized(gate) {
      buffer.clear()
    }
  }
}
