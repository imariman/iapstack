/// Requires one non-empty customer identity in valid Unicode without
/// surrounding whitespace or control characters.
///
/// This mirrors the server's identifier rule (`validateIdentifier` in
/// `internal/core/identifiers.go`): valid UTF-8, which for a Dart string
/// means no unpaired surrogate (UTF-8 encoding would replace it with U+FFFD),
/// and no C0 (U+0000-U+001F), DEL, or C1 (U+0080-U+009F) control character.
/// It runs before the Huawei purchase UI opens: an id the server rejects
/// would be charged as `developerPayload` and then never produce an
/// entitlement, and a padded id on restore would never match the bound
/// payload and silently skip rows.
void validateExternalCustomerId(String value) {
  if (value.isEmpty ||
      value.trim() != value ||
      value.runes.any(_isRejectedRune)) {
    throw ArgumentError.value(
        value,
        'externalCustomerId',
        'must be non-empty valid Unicode without '
            'surrounding whitespace or control characters');
  }
}

/// A control character, or an unpaired surrogate ([String.runes] yields
/// those as their own code unit).
bool _isRejectedRune(int rune) =>
    rune <= 0x1f ||
    (rune >= 0x7f && rune <= 0x9f) ||
    (rune >= 0xd800 && rune <= 0xdfff);
