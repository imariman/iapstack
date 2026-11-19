package com.iapstack.core

/**
 * Marks declarations that are public only so the IAPStack Google Play and
 * Huawei modules and the React Native Android bridge can share them with core.
 *
 * They are not public API: they can change or disappear in any release without
 * notice. Application code must not opt in.
 */
@RequiresOptIn(
  message = "Internal to the IAPStack SDK modules. Not public API; it can change without notice.",
  level = RequiresOptIn.Level.ERROR,
)
@Retention(AnnotationRetention.BINARY)
@Target(AnnotationTarget.CLASS, AnnotationTarget.FUNCTION, AnnotationTarget.PROPERTY)
annotation class InternalIapStackApi
