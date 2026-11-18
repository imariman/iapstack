/// Requires one non-empty customer identity without surrounding whitespace or
/// control characters.
///
/// This mirrors the server's identifier rule (`validateIdentifier` in
/// `internal/core/identifiers.go`), whose control characters are C0
/// (U+0000-U+001F), DEL, and C1 (U+0080-U+009F). It runs before the Huawei
/// purchase UI opens: an id the server rejects would be charged as
/// `developerPayload` and then never produce an entitlement, and a padded id
/// on restore would never match the bound payload and silently skip rows.
void validateExternalCustomerId(String value) {
  if (value.isEmpty ||
      value.trim() != value ||
      value.runes.any(_isControlCharacter)) {
    throw ArgumentError.value(value, 'externalCustomerId',
        'must be non-empty, without surrounding whitespace or control characters');
  }
}

bool _isControlCharacter(int rune) =>
    rune <= 0x1f || (rune >= 0x7f && rune <= 0x9f);
