import 'dart:convert';
import 'dart:io';

/// One case from the shared `contracts/test-vectors/external-customer-ids.json`.
final class CustomerIdVector {
  const CustomerIdVector(this.name, this.value, {required this.accepted});

  final String name;
  final String value;

  /// Whether the server's identifier rule accepts [value].
  final bool accepted;
}

/// Loads the shared cases. Number segments are UTF-16 code units, so lone
/// surrogates and control characters survive JSON parsing.
List<CustomerIdVector> loadCustomerIdVectors() {
  final file =
      File('../../../contracts/test-vectors/external-customer-ids.json');
  final document = jsonDecode(file.readAsStringSync()) as Map<String, Object?>;
  final cases = document['cases']! as List<Object?>;
  if (cases.isEmpty) {
    throw StateError('The shared customer ID vectors are empty.');
  }
  return <CustomerIdVector>[
    for (final item in cases.cast<Map<String, Object?>>())
      CustomerIdVector(
        item['name']! as String,
        String.fromCharCodes(<int>[
          for (final segment in item['value']! as List<Object?>)
            if (segment is int) segment else ...(segment! as String).codeUnits,
        ]),
        accepted: item['accepted']! as bool,
      ),
  ];
}
