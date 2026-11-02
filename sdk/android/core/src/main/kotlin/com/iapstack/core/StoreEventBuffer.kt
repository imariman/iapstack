package com.iapstack.core

/**
 * Holds store events that arrive before a React Native listener attaches.
 *
 * The oldest event is dropped after [limit] so a session that never subscribes
 * cannot grow the queue without bound. The React Native Android module compiles
 * these sources and is the caller.
 */
internal class StoreEventBuffer<T>(private val limit: Int = 32) {
  private val events = ArrayDeque<T>()

  init {
    require(limit > 0)
  }

  fun enqueue(event: T) {
    synchronized(events) {
      events.addLast(event)
      while (events.size > limit) {
        events.removeFirst()
      }
    }
  }

  fun drain(): List<T> = synchronized(events) {
    val copy = events.toList()
    events.clear()
    copy
  }

  fun clear() {
    synchronized(events) {
      events.clear()
    }
  }
}
