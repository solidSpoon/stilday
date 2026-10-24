// Package artifact reads and writes the day's events as one JSON object per
// line in a yearly JSONL file, and folds older per-day files into it.
package artifact

import (
	"encoding/json"
	"strings"
	"time"
)

// Entry is one line of a day's analysis:
//
//	{"occurred_at":"2026-10-23T14:05:00+08:00","updated_at":"2026-10-24T09:12:00+08:00","day":"2026-10-23","kind":"activity","summary":"在 Blue Bottle 写了两个小时代码"}
//
// occurred_at is when the event happened; updated_at is when this line was
// last written; day is the day it was analyzed into. The day is what a
// re-analysis replaces, even when the event happened on another date.
type Entry struct {
	// Seq identifies the entry in this run, for keys in the view.
	Seq        int64
	OccurredAt time.Time
	UpdatedAt  time.Time
	Day        string
	Kind       string
	Summary    string
}

// entryRecord is the artifact line as it is written and read. New lines use
// only the first five fields; ts, source and data are the shapes earlier
// versions wrote and are still accepted for reading.
type entryRecord struct {
	OccurredAt string         `json:"occurred_at"`
	UpdatedAt  string         `json:"updated_at"`
	Day        string         `json:"day"`
	Kind       string         `json:"kind"`
	Summary    string         `json:"summary"`
	TS         string         `json:"ts,omitempty"`
	Source     string         `json:"source,omitempty"`
	Data       map[string]any `json:"data,omitempty"`
}

// DayFormat is the key of one day wherever the app names a day: folder
// names, artifact lines, queue entries and calendar marks.
const DayFormat = "2006-01-02"

// DayKey formats a day as YYYY-MM-DD. The zero time has no key.
func DayKey(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(DayFormat)
}

// ParseDayKey reads a YYYY-MM-DD key in local time.
func ParseDayKey(key string) (time.Time, error) {
	return time.ParseInLocation(DayFormat, key, time.Local)
}

// DayKey is the day this line was analyzed into, falling back to the day it
// happened when an older line has no day.
func (e Entry) DayKey() string {
	if e.Day != "" {
		return e.Day
	}
	if e.OccurredAt.IsZero() {
		return ""
	}
	return DayKey(e.OccurredAt)
}

// OnOtherDay reports whether the event happened on a date other than the
// day it was written into, which the cards mark with the event's own date.
func (e Entry) OnOtherDay() bool {
	return e.Day != "" && e.Day != DayKey(e.OccurredAt)
}

// record is the on-disk representation, with Day and UpdatedAt filled in so
// every line says what a re-analysis should replace.
func (e Entry) record() entryRecord {
	r := entryRecord{
		OccurredAt: e.OccurredAt.Format(time.RFC3339),
		UpdatedAt:  e.UpdatedAt.Format(time.RFC3339),
		Day:        e.Day,
		Kind:       e.Kind,
		Summary:    e.Summary,
	}
	if r.Day == "" {
		r.Day = DayKey(e.OccurredAt)
	}
	if e.UpdatedAt.IsZero() {
		r.UpdatedAt = r.OccurredAt
	}
	return r
}

// entry decodes a line. fallbackDay and fallbackUpdated stand in for the
// fields older files left out: the file's day and modification time.
func (r entryRecord) entry(fallbackDay string, fallbackUpdated time.Time) (Entry, bool) {
	occurred := r.OccurredAt
	if occurred == "" {
		occurred = r.TS
	}
	at, err := time.Parse(time.RFC3339, occurred)
	if err != nil {
		return Entry{}, false
	}
	updated := at
	if r.UpdatedAt != "" {
		if t, err := time.Parse(time.RFC3339, r.UpdatedAt); err == nil {
			updated = t
		}
	}
	if fallbackUpdated.After(updated) {
		updated = fallbackUpdated
	}
	day := r.Day
	if day == "" {
		day = fallbackDay
	}
	if day == "" {
		day = DayKey(at)
	}
	summary := r.Summary
	if summary == "" {
		summary = r.legacySummary()
	}
	return Entry{OccurredAt: at, UpdatedAt: updated, Day: day, Kind: r.Kind, Summary: summary}, true
}

// legacySummary reads the old shape, whose description lived in
// data.summary, so earlier artifacts still show on the board.
func (r entryRecord) legacySummary() string {
	if r.Summary != "" || r.Data == nil {
		return r.Summary
	}
	if s, ok := r.Data["summary"].(string); ok {
		return s
	}
	return ""
}

// Line returns the entry as the single JSON line written to the artifact.
// encoding/json escapes the summary correctly; HTML escaping is off so the
// file stays readable.
func (e Entry) Line() string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	// A record of strings and times cannot fail to encode.
	_ = enc.Encode(e.record())
	return strings.TrimSuffix(b.String(), "\n")
}
