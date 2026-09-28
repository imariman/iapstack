package iapstack

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Retry-After grammar shared by every IAPStack SDK: delay-seconds of one to
// nine ASCII digits, or an IMF-fixdate HTTP-date (RFC 9110 section 5.6.7)
// whose fields, including the weekday, round-trip through the calendar.
var (
	delaySecondsPattern = regexp.MustCompile(`^[0-9]{1,9}$`)
	imfFixdatePattern   = regexp.MustCompile(
		`^(Mon|Tue|Wed|Thu|Fri|Sat|Sun), ([0-9]{2}) (Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) ([0-9]{4}) ([0-9]{2}):([0-9]{2}):([0-9]{2}) GMT$`,
	)
	imfMonths = []string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
)

// retryAfterFrom parses the Retry-After header of one response.
//
// It returns zero when the header is absent or malformed and when its
// HTTP-date has already elapsed; see APIError.RetryAfter.
func retryAfterFrom(header http.Header, now time.Time) time.Duration {
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		return 0
	}
	if delaySecondsPattern.MatchString(value) {
		seconds, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	at, ok := parseIMFFixdate(value)
	if !ok {
		return 0
	}
	if delay := at.Sub(now); delay > 0 {
		return delay
	}
	return 0
}

// parseIMFFixdate accepts only a civil date whose fields round-trip.
func parseIMFFixdate(value string) (time.Time, bool) {
	match := imfFixdatePattern.FindStringSubmatch(value)
	if match == nil {
		return time.Time{}, false
	}
	day, _ := strconv.Atoi(match[2])
	month := 0
	for index, name := range imfMonths {
		if name == match[3] {
			month = index + 1
		}
	}
	year, _ := strconv.Atoi(match[4])
	hour, _ := strconv.Atoi(match[5])
	minute, _ := strconv.Atoi(match[6])
	second, _ := strconv.Atoi(match[7])
	if hour > 23 || minute > 59 || second > 59 {
		return time.Time{}, false
	}
	at := time.Date(year, time.Month(month), day, hour, minute, second, 0, time.UTC)
	if at.Year() != year || at.Month() != time.Month(month) || at.Day() != day {
		return time.Time{}, false
	}
	if at.Weekday().String()[:3] != match[1] {
		return time.Time{}, false
	}
	return at, true
}
