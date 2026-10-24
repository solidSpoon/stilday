package artifact

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The file is the AI's view of the day: it comes back as it was written.
func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	now := time.Now()
	e := Entry{OccurredAt: now, UpdatedAt: now, Day: now.Format(DayFormat), Kind: "observation", Summary: "hello"}
	if err := s.Append(e); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, now.Format("2006")+".jsonl")
	if s.Path(now) != want {
		t.Errorf("path is %s, want %s", s.Path(now), want)
	}
	entries, err := s.Load(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Line() != e.Line() {
		t.Fatalf("loaded %+v, wrote %s", entries, e.Line())
	}
	if all, err := s.LoadAll(); err != nil || len(all) != 1 {
		t.Errorf("loadAll = %d entries, %v", len(all), err)
	}
}

// The previous artifact shape, with data.summary and source, still loads.
func TestLoadLegacyEntry(t *testing.T) {
	day := time.Date(2026, 10, 23, 12, 0, 0, 0, time.Local)
	s := Open(t.TempDir())
	path := s.Path(day)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"ts":"2026-10-23T08:41:41+08:00","source":"ai","kind":"activity","data":{"summary":"早上在 BOSS直聘 上看公司"}}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := s.Load(day)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("%d entries", len(entries))
	}
	e := entries[0]
	if e.Day != "2026-10-23" || e.Kind != "activity" || e.Summary != "早上在 BOSS直聘 上看公司" {
		t.Errorf("loaded %+v", e)
	}
}

func TestStoreMigratesPerDayArtifacts(t *testing.T) {
	dir := t.TempDir()
	day := time.Date(2026, 10, 23, 12, 0, 0, 0, time.Local)
	old := filepath.Join(dir, "2026", "10", "2026-10-23.jsonl")
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"ts":"2026-10-23T08:41:41+08:00","source":"ai","kind":"activity","data":{"summary":"早上在 BOSS直聘 上看公司"}}`
	if err := os.WriteFile(old, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := Open(dir)
	entries, err := s.Load(day)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("%d entries after migration", len(entries))
	}
	e := entries[0]
	if e.Day != "2026-10-23" || e.Kind != "activity" || e.Summary != "早上在 BOSS直聘 上看公司" {
		t.Errorf("migrated %+v", e)
	}
	if e.OccurredAt.IsZero() || e.UpdatedAt.IsZero() {
		t.Errorf("times were not filled: %+v", e)
	}
	yearFile := filepath.Join(dir, "2026.jsonl")
	b, err := os.ReadFile(yearFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(string(b)), strings.ToLower(`"occurred_at"`)) ||
		!strings.Contains(strings.ToLower(string(b)), strings.ToLower(`"updated_at"`)) {
		t.Errorf("year file fields: %s", b)
	}
	if strings.Contains(strings.ToLower(string(b)), `"ts":`) {
		t.Errorf("old ts field survived: %s", b)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("old day file still exists: %v", err)
	}
}
