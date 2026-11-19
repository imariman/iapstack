package com.iapstack.core

/**
 * Requires one non-empty customer identity in valid Unicode without
 * surrounding whitespace or control characters.
 *
 * This mirrors the server's identifier rule (`validateIdentifier` in
 * `internal/core/identifiers.go`), so an ID the server would reject fails
 * before the store charges or a request is sent: valid UTF-8, which for a
 * Kotlin string means no unpaired surrogate (UTF-8 encoding would replace it),
 * no C0 (U+0000-U+001F), DEL, or C1 (U+0080-U+009F) control character (the
 * [Char.isISOControl] set), and no leading or trailing whitespace as Go's
 * `strings.TrimSpace` defines it. The shared cases in
 * `contracts/test-vectors/external-customer-ids.json` pin it to the server.
 *
 * The one Kotlin copy of the rule: core, the Google Play and Huawei modules,
 * and the React Native Android bridge all call it. Not public API, see
 * [InternalIapStackApi].
 */
@InternalIapStackApi
fun requireExternalCustomerId(value: String) {
  if (value.isEmpty() ||
    isGoSpace(value.first()) ||
    isGoSpace(value.last()) ||
    value.any { it.isISOControl() } ||
    hasUnpairedSurrogate(value)
  ) {
    throw IllegalArgumentException(
      "externalCustomerId must be non-empty valid Unicode without surrounding whitespace or control characters",
    )
  }
}

/**
 * Go's `unicode.IsSpace`, which the server's `strings.TrimSpace` strips. An
 * explicit set keeps the edge check independent of the runtime's Unicode
 * tables, which [String.trim] depends on. Every member is one UTF-16 unit.
 */
private fun isGoSpace(char: Char): Boolean = when (char.code) {
  in 0x09..0x0d, 0x20, 0x85, 0xa0, 0x1680, in 0x2000..0x200a, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000 -> true
  else -> false
}

private fun hasUnpairedSurrogate(value: String): Boolean {
  var index = 0
  while (index < value.length) {
    val char = value[index]
    if (char.isHighSurrogate() && index + 1 < value.length && value[index + 1].isLowSurrogate()) {
      index += 2
      continue
    }
    if (char.isSurrogate()) return true
    index++
  }
  return false
}
