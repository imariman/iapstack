package com.iapstack.huawei

/**
 * Safe Huawei device SDK or evidence failure.
 */
class HuaweiIapStackException(
  val code: String,
  message: String,
  val userCancelled: Boolean = false,
  cause: Throwable? = null,
) : Exception(message, cause) {
  override fun toString() = "HuaweiIapStackException($code): $message"
}
