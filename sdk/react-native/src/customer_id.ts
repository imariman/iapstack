/**
 * Reports whether a value is one non-empty customer identity in valid Unicode
 * without surrounding whitespace or control characters.
 *
 * This mirrors the server's identifier rule (`validateIdentifier` in
 * `internal/core/identifiers.go`). Valid UTF-8 means no unpaired surrogate
 * here: UTF-8 encoding would replace it with U+FFFD, so the server would see a
 * different ID. Control characters are C0 (U+0000-U+001F), DEL, and C1
 * (U+0080-U+009F). The shared cases in
 * `contracts/test-vectors/external-customer-ids.json` pin this copy to the
 * server. Internal: not exported from the package entry points.
 */
export function isValidExternalCustomerId(value: unknown): value is string {
  if (typeof value !== 'string' || !value ||
      isGoSpace(value.charCodeAt(0)) || isGoSpace(value.charCodeAt(value.length - 1))) {
    return false;
  }
  for (let index = 0; index < value.length; index++) {
    const unit = value.charCodeAt(index);
    if (unit <= 0x1f || (unit >= 0x7f && unit <= 0x9f)) return false;
    if (unit >= 0xd800 && unit <= 0xdbff) {
      const next = value.charCodeAt(index + 1);
      if (!(next >= 0xdc00 && next <= 0xdfff)) return false;
      index++;
    } else if (unit >= 0xdc00 && unit <= 0xdfff) {
      return false;
    }
  }
  return true;
}

/** Throws before the store charges or a request is sent for an ID the server would reject. */
export function validateExternalCustomerId(value: unknown): void {
  if (!isValidExternalCustomerId(value)) {
    throw new TypeError(
      'externalCustomerId must be non-empty valid Unicode without surrounding whitespace or control characters');
  }
}

/**
 * Go's `unicode.IsSpace`, which the server's `strings.TrimSpace` strips.
 * `String.prototype.trim` differs: it also strips U+FEFF, which the server
 * keeps. Every member is one UTF-16 code unit.
 */
function isGoSpace(unit: number): boolean {
  return (unit >= 0x09 && unit <= 0x0d) || unit === 0x20 || unit === 0x85 || unit === 0xa0 ||
    unit === 0x1680 || (unit >= 0x2000 && unit <= 0x200a) || unit === 0x2028 || unit === 0x2029 ||
    unit === 0x202f || unit === 0x205f || unit === 0x3000;
}
