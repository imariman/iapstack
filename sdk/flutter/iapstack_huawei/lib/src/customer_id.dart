/// Requires one non-empty customer identity in valid Unicode without
/// surrounding whitespace or control characters.
///
/// This mirrors the server's identifier rule (`validateIdentifier` in
/// `internal/core/identifiers.go`): valid UTF-8, which for a Dart string
/// means no unpaired surrogate (UTF-8 encoding would replace it with U+FFFD),
/// no C0 (U+0000-U+001F), DEL, or C1 (U+0080-U+009F) control character, and
/// no leading or trailing whitespace as Go's `strings.TrimSpace` defines it.
/// It runs before the Huawei purchase UI opens: an id the server rejects
/// would be charged as `developerPayload` and then never produce an
/// entitlement, and a padded id on restore would never match the bound
/// payload and silently skip rows. The shared cases in
/// `contracts/test-vectors/external-customer-ids.json` pin this copy to the
/// server.
void validateExternalCustomerId(String value) {
  if (value.isEmpty ||
      _isGoSpace(value.codeUnitAt(0)) ||
      _isGoSpace(value.codeUnitAt(value.length - 1)) ||
      value.runes.any(_isRejectedRune)) {
    throw ArgumentError.value(
        value,
        'externalCustomerId',
        'must be non-empty valid Unicode without '
            'surrounding whitespace or control characters');
  }
}

/// Go's `unicode.IsSpace`, which the server's `strings.TrimSpace` strips.
/// [String.trim] differs: it also strips U+FEFF, which the server keeps. Every
/// member is one UTF-16 code unit.
bool _isGoSpace(int unit) =>
    (unit >= 0x09 && unit <= 0x0d) ||
    unit == 0x20 ||
    unit == 0x85 ||
    unit == 0xa0 ||
    unit == 0x1680 ||
    (unit >= 0x2000 && unit <= 0x200a) ||
    unit == 0x2028 ||
    unit == 0x2029 ||
    unit == 0x202f ||
    unit == 0x205f ||
    unit == 0x3000;

/// A control character, or an unpaired surrogate ([String.runes] yields
/// those as their own code unit).
bool _isRejectedRune(int rune) =>
    rune <= 0x1f ||
    (rune >= 0x7f && rune <= 0x9f) ||
    (rune >= 0xd800 && rune <= 0xdfff);
