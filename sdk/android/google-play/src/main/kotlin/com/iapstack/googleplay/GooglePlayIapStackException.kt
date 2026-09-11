package com.iapstack.googleplay

/**
 * Safe Google Play companion, plugin, or Billing result failure.
 */
class GooglePlayIapStackException(
  val code: String,
  message: String,
  val userCancelled: Boolean = false,
  cause: Throwable? = null,
) : Exception(message, cause) {
  override fun toString() = "GooglePlayIapStackException($code): $message"
}
