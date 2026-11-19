/// Helpers shared by this package and its companions (`iapstack_apple`,
/// `iapstack_google_play`, `iapstack_huawei`).
///
/// Not public API: `package:iapstack/iapstack.dart` does not export this
/// library, and it can change or disappear in any release without notice.
/// Application code must not import it.
library;

export 'src/customer_id.dart' show validateExternalCustomerId;
