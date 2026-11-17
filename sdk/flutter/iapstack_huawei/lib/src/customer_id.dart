/// Requires one non-empty customer identity without surrounding whitespace.
///
/// The same rule as core verification. A padded id would be charged as
/// `developerPayload` and then rejected before an entitlement is written, or
/// on restore would never match the bound payload and silently skip rows.
void validateExternalCustomerId(String value) {
  if (value.isEmpty || value.trim() != value) {
    throw ArgumentError.value(value, 'externalCustomerId',
        'must be non-empty without surrounding whitespace');
  }
}
