package artifact

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A summary with quotes, markup and control characters must still produce
// one valid JSON line: the old hand-built fmt.Sprintf line wrote \x escapes
// that no JSON reader accepts.
func TestEntryLineIsValidJSON(t *testing.T) {
	at := time.Date(2026, 10, 23, 14, 5, 0, 0, time.Local)
	e := Entry{
		OccurredAt: at,
		UpdatedAt:  at.Add(time.Hour),
		Day:        "2026-10-23",
		Kind:       "place",
		Summary:    "他说 \"走吧\" & <b>午餐</b>\n第二行\t带制表符\x01",
	}
	line := e.Line()
	if strings.ContainsRune(line, '\n') {
		t.Fatalf("line has a raw newline: %q", line)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("line is not JSON: %v: %s", err, line)
	}
	if got["summary"] != e.Summary {
		t.Errorf("summary round-trips as %q", got["summary"])
	}
	if got["day"] != "2026-10-23" || got["kind"] != "place" {
		t.Errorf("line = %s", line)
	}
	var r entryRecord
	if err := json.Unmarshal([]byte(line), &r); err != nil {
		t.Fatal(err)
	}
	back, ok := r.entry("", time.Time{})
	if !ok || back.Summary != e.Summary || !back.OccurredAt.Equal(at) {
		t.Errorf("decoded %+v", back)
	}
}

// A line without a day falls back to the day it happened, and the old
// data.summary shape still reads.
func TestEntryRecordLegacyFallbacks(t *testing.T) {
	r := entryRecord{
		TS:      "2026-10-23T08:41:41+08:00",
		Source:  "ai",
		Kind:    "activity",
		Data:    map[string]any{"summary": "早餐"},
		Summary: "",
	}
	e, ok := r.entry("", time.Time{})
	if !ok {
		t.Fatal("legacy record did not decode")
	}
	if e.Day != "2026-10-23" || e.Summary != "早餐" {
		t.Errorf("legacy record = %+v", e)
	}
}

func TestEntryOnOtherDay(t *testing.T) {
	at := time.Date(2026, 10, 23, 0, 5, 0, 0, time.Local)
	if !(Entry{OccurredAt: at, Day: "2026-10-22"}).OnOtherDay() {
		t.Error("a late-night event written into the previous day is not marked")
	}
	if (Entry{OccurredAt: at, Day: "2026-10-23"}).OnOtherDay() {
		t.Error("same-day event marked as other-day")
	}
}
