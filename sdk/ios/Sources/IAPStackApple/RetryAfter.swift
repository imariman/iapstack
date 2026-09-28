import Foundation

private let delaySeconds = try! NSRegularExpression(pattern: "^[0-9]{1,9}$")
private let imfFixdate = try! NSRegularExpression(
  pattern: "^(Mon|Tue|Wed|Thu|Fri|Sat|Sun), ([0-9]{2}) (Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) ([0-9]{4}) ([0-9]{2}):([0-9]{2}):([0-9]{2}) GMT$",
)
/// Indexed by `Calendar` weekday, where 1 is Sunday.
private let weekdays = ["", "Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"]
private let months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"]

/// Parses a `Retry-After` header with the grammar every IAPStack SDK shares:
/// delay-seconds of one to nine ASCII digits, or an IMF-fixdate HTTP-date
/// (RFC 9110 section 5.6.7) whose fields, including the weekday, round-trip
/// through the calendar. The date is resolved on a Gregorian calendar pinned
/// to GMT, so the device calendar (Buddhist, Japanese, ...) cannot shift it.
///
/// Returns the cooldown in seconds, 0 for a date that elapsed before `now`,
/// and nil for an absent or malformed header.
func parseRetryAfter(_ header: String?, now: Date) -> TimeInterval? {
  guard let header else { return nil }
  let value = header.trimmingCharacters(in: .whitespacesAndNewlines)
  if value.isEmpty {
    return nil
  }
  let range = NSRange(value.startIndex..., in: value)
  if delaySeconds.firstMatch(in: value, range: range) != nil, let seconds = Double(value) {
    return seconds
  }
  guard let match = imfFixdate.firstMatch(in: value, range: range) else {
    return nil
  }
  func group(_ index: Int) -> String {
    String(value[Range(match.range(at: index), in: value)!])
  }
  var components = DateComponents()
  components.year = Int(group(4))
  components.month = months.firstIndex(of: group(3)).map { $0 + 1 }
  components.day = Int(group(2))
  components.hour = Int(group(5))
  components.minute = Int(group(6))
  components.second = Int(group(7))
  guard let hour = components.hour, hour <= 23,
        let minute = components.minute, minute <= 59,
        let second = components.second, second <= 59
  else {
    return nil
  }
  var calendar = Calendar(identifier: .gregorian)
  calendar.timeZone = TimeZone(secondsFromGMT: 0)!
  guard let at = calendar.date(from: components) else {
    return nil
  }
  let resolved = calendar.dateComponents([.year, .month, .day, .weekday], from: at)
  guard resolved.year == components.year,
        resolved.month == components.month,
        resolved.day == components.day,
        let weekday = resolved.weekday, weekdays[weekday] == group(1)
  else {
    return nil
  }
  return max(0, at.timeIntervalSince(now))
}
