final RegExp _delaySeconds = RegExp(r'^\d{1,9}$');
final RegExp _imfFixdate = RegExp(
  r'^(Mon|Tue|Wed|Thu|Fri|Sat|Sun), (\d{2}) '
  r'(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) (\d{4}) '
  r'(\d{2}):(\d{2}):(\d{2}) GMT$',
);
const List<String> _weekdays = <String>[
  'Mon',
  'Tue',
  'Wed',
  'Thu',
  'Fri',
  'Sat',
  'Sun',
];
const List<String> _months = <String>[
  'Jan',
  'Feb',
  'Mar',
  'Apr',
  'May',
  'Jun',
  'Jul',
  'Aug',
  'Sep',
  'Oct',
  'Nov',
  'Dec',
];

/// Parses a `Retry-After` header with the grammar every IAPStack SDK shares:
/// delay-seconds of one to nine ASCII digits, or an IMF-fixdate HTTP-date
/// (RFC 9110 section 5.6.7) whose fields, including the weekday, round-trip
/// through the calendar, so `31 Feb` is malformed rather than 3 March.
///
/// Returns the cooldown, [Duration.zero] for a date that elapsed before [now],
/// and null for an absent or malformed header.
Duration? parseRetryAfter(String? header, DateTime now) {
  final value = header?.trim() ?? '';
  if (value.isEmpty) {
    return null;
  }
  if (_delaySeconds.hasMatch(value)) {
    return Duration(seconds: int.parse(value));
  }
  final match = _imfFixdate.firstMatch(value);
  if (match == null) {
    return null;
  }
  final weekday = match.group(1)!;
  final day = int.parse(match.group(2)!);
  final month = _months.indexOf(match.group(3)!) + 1;
  final year = int.parse(match.group(4)!);
  final hour = int.parse(match.group(5)!);
  final minute = int.parse(match.group(6)!);
  final second = int.parse(match.group(7)!);
  if (hour > 23 || minute > 59 || second > 59) {
    return null;
  }
  final at = DateTime.utc(year, month, day, hour, minute, second);
  if (at.year != year ||
      at.month != month ||
      at.day != day ||
      _weekdays[at.weekday - 1] != weekday) {
    return null;
  }
  final remaining = at.difference(now.toUtc());
  return remaining.isNegative ? Duration.zero : remaining;
}
