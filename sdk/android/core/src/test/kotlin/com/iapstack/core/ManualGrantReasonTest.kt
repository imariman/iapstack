package com.iapstack.core

import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/** An administrator's manual grant reports the manual_grant reason and is time-bounded like any projection. */
class ManualGrantReasonTest {
  @Test
  fun manualGrantReasonDecodesAndGrantsAccessUntilTheEnd() {
    val entitlement = Entitlement.fromJson(
      mapOf(
        "key" to "premium",
        "access" to "allowed",
        "reason" to "manual_grant",
        "effective_starts_at" to "2026-11-18T10:00:00Z",
        "effective_ends_at" to "2026-12-18T10:00:00Z",
        "version" to 2,
      ),
    )

    assertEquals("manual_grant", entitlement.reason)
    assertTrue(entitlement.grantsAccessAt(Instant.parse("2026-12-01T00:00:00Z")))
    assertFalse(entitlement.grantsAccessAt(Instant.parse("2026-12-18T10:00:00Z")))
  }
}
