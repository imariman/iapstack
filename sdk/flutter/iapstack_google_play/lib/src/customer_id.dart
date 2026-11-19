import 'package:iapstack/internal.dart';

/// Play Billing's limit for `obfuscatedAccountId`, which carries the
/// customer ID.
const int _maxObfuscatedAccountIdLength = 64;

/// Applies core's shared customer ID rule ([validateExternalCustomerId], the
/// server's identifier rule) plus Play's own 64-character
/// `obfuscatedAccountId` limit.
///
/// It runs before Play Billing opens: an ID the server rejects would be
/// charged and then never produce an entitlement.
void validateGooglePlayCustomerId(String value) {
  validateExternalCustomerId(value);
  if (value.length > _maxObfuscatedAccountIdLength) {
    throw ArgumentError.value(
      value,
      'externalCustomerId',
      'must be at most 64 characters, the Google Play obfuscatedAccountId '
          'limit',
    );
  }
}
