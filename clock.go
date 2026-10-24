package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"stilday/internal/artifact"
)

// dayKey formats a day as YYYY-MM-DD; the artifact package owns the shape.
func dayKey(t time.Time) string { return artifact.DayKey(t) }

// parseDayKey reads a YYYY-MM-DD key in local time.
func parseDayKey(key string) (time.Time, error) { return artifact.ParseDayKey(key) }

func midnight(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func sameDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day()
}

// parseClock reads "HH:MM" as a duration since midnight.
func parseClock(s string) (time.Duration, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("%q 不是 HH:MM", s)
	}
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute, nil
}

// clockOn returns the time "HH:MM" on a given day.
func clockOn(day time.Time, hhmm string) (time.Time, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(hhmm))
	if err != nil {
		return time.Time{}, fmt.Errorf("%q 不是 HH:MM", hhmm)
	}
	return time.Date(day.Year(), day.Month(), day.Day(), t.Hour(), t.Minute(), 0, 0, day.Location()), nil
}

// normalizeClock accepts H:MM or HH:MM and returns HH:MM, so typing
// "2:00" is not treated as an error. Anything else is returned trimmed.
func normalizeClock(s string) string {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok {
		return strings.TrimSpace(s)
	}
	hh, err1 := strconv.Atoi(strings.TrimSpace(h))
	mm, err2 := strconv.Atoi(strings.TrimSpace(m))
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return strings.TrimSpace(s)
	}
	return fmt.Sprintf("%02d:%02d", hh, mm)
}

// clockText formats an RFC 3339 timestamp as the time of day, or returns it
// unchanged when it is not a timestamp.
func clockText(rfc string) string {
	if t, err := time.Parse(time.RFC3339, rfc); err == nil {
		return t.Format("15:04")
	}
	return rfc
}

// dayLabel names a day in full.
func dayLabel(t time.Time) string {
	return fmt.Sprintf("%d 年 %d 月 %d 日 · %s", t.Year(), int(t.Month()), t.Day(), weekday(t))
}

// weekday names the weekday of a day.
func weekday(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[t.Weekday()]
}
