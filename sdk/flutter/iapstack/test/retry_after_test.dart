import 'package:iapstack/src/retry_after.dart';
import 'package:test/test.dart';

void main() {
  group('parseRetryAfter', () {
    final now = DateTime.utc(2026, 9, 28, 12);

    test('accepts delay-seconds and round-tripping IMF-fixdates only', () {
      final cases = <(String?, Duration?)>[
        (null, null),
        ('', null),
        ('17', const Duration(seconds: 17)),
        (' 5 ', const Duration(seconds: 5)),
        ('999999999', const Duration(seconds: 999999999)),
        ('1000000000', null),
        (
          'Fri, 02 Oct 2026 00:00:00 GMT',
          DateTime.utc(2026, 10, 2).difference(now),
        ),
        ('Mon, 28 Sep 2026 11:59:00 GMT', Duration.zero),
        ('Fri, 31 Feb 2026 00:00:00 GMT', null),
        ('Wed, 31 Apr 2026 00:00:00 GMT', null),
        ('Mon, 02 Oct 2026 00:00:00 GMT', null),
        ('Friday, 02-Oct-26 00:00:00 GMT', null),
        ('Fri Oct  2 00:00:00 2026', null),
        ('Fri, 02 Oct 2026 00:00:00 +0000', null),
        ('02 Oct 2026 00:00:00 GMT', null),
        ('Fri, 02 Oct 2026 24:00:00 GMT', null),
        ('-3', null),
        ('1.5', null),
        ('March 1, 2026', null),
        ('soon', null),
      ];
      for (final (header, expected) in cases) {
        expect(parseRetryAfter(header, now), expected,
            reason: 'Retry-After $header');
      }
    });
  });
}
