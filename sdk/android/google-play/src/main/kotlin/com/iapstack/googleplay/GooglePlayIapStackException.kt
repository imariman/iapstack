package com.iapstack.googleplay

/**
 * Safe Google Play companion, plugin, or Billing result failure.
 */
class GooglePlayIapStackException(
  val code: String,
  message: String,
  val userCancelled: Boolean = false,
  /**
   * Whether the same call should be retried once.
   *
   * Set for a Play Billing service disconnect. The adapter starts one
   * reconnect itself. The in-flight call still fails because its Billing
   * callback belonged to the dropped connection.
   */
  val retryable: Boolean = false,
  cause: Throwable? = null,
) : Exception(message, cause) {
  override fun toString() = "GooglePlayIapStackException($code): $message"
}
