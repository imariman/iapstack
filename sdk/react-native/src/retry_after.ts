const DELAY_SECONDS = /^\d{1,9}$/;
const IMF_FIXDATE =
  /^(Mon|Tue|Wed|Thu|Fri|Sat|Sun), (\d{2}) (Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) (\d{4}) (\d{2}):(\d{2}):(\d{2}) GMT$/;
const WEEKDAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

/**
 * Parses a Retry-After header with the grammar every IAPStack SDK shares:
 * delay-seconds of one to nine ASCII digits, or an IMF-fixdate HTTP-date
 * (RFC 9110 section 5.6.7) whose fields, including the weekday, round-trip
 * through the calendar. The date is built with `Date.UTC`, never `Date.parse`,
 * so engine-specific leniency cannot turn `31 Feb` into a real cooldown.
 *
 * Returns the cooldown in milliseconds, 0 for an elapsed date, and undefined
 * for an absent or malformed header.
 */
export function parseRetryAfter(header: string | null | undefined, nowMs: number): number | undefined {
  const value = header?.trim() ?? '';
  if (!value) {
    return undefined;
  }
  if (DELAY_SECONDS.test(value)) {
    return Number.parseInt(value, 10) * 1000;
  }
  const match = IMF_FIXDATE.exec(value);
  if (!match) {
    return undefined;
  }
  const [, weekday, dayText, monthText, yearText, hourText, minuteText, secondText] = match;
  const day = Number(dayText);
  const month = MONTHS.indexOf(monthText ?? '');
  const year = Number(yearText);
  const hour = Number(hourText);
  const minute = Number(minuteText);
  const second = Number(secondText);
  if (hour > 23 || minute > 59 || second > 59) {
    return undefined;
  }
  const at = new Date(Date.UTC(year, month, day, hour, minute, second));
  if (
    at.getUTCFullYear() !== year ||
    at.getUTCMonth() !== month ||
    at.getUTCDate() !== day ||
    WEEKDAYS[at.getUTCDay()] !== weekday
  ) {
    return undefined;
  }
  return Math.max(0, at.getTime() - nowMs);
}
