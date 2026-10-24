package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestDayTimeline(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	a.addEntry(Entry{OccurredAt: a.day.Add(14 * time.Hour), Day: a.day.Format("2006-01-02"),
		Kind: "place", Summary: "在 Blue Bottle 写了两个小时代码"})
	a.addEntry(Entry{OccurredAt: a.day.Add(9 * time.Hour), Day: a.day.Format("2006-01-02"),
		Kind: "task", Summary: "完成了照片合集原型"})
	tt := ui.NewTester(a.view, 1300, 820)
	tt.Frame()
	if !tt.HasText("在 Blue Bottle 写了两个小时代码") || !tt.HasText("完成了照片合集原型") {
		t.Fatalf("the output timeline is missing: %q", tt.Texts())
	}
	if !tt.HasText("重新分析") {
		t.Errorf("no re-analyze button: %q", tt.Texts())
	}
}

func TestClearAnalysis(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	a.addEntry(Entry{OccurredAt: a.day.Add(10 * time.Hour), Day: a.day.Format("2006-01-02"),
		Kind: "place", Summary: "咖啡"})
	dir := a.cfg.workDayDir(a.day)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(dir, "chat.jsonl")
	if err := os.WriteFile(transcript, []byte(`{"role":"user","text":"hi"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".pi"), 0o755); err != nil {
		t.Fatal(err)
	}
	a.clearAnalysis()
	if len(a.entries) != 0 {
		t.Errorf("%d entries after clearing", len(a.entries))
	}
	if entries, err := a.store.Load(a.day); err != nil || len(entries) != 0 {
		t.Errorf("day entries after clearing: %v, %v", entries, err)
	}
	if _, err := os.Stat(transcript); !os.IsNotExist(err) {
		t.Errorf("transcript still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".pi")); !os.IsNotExist(err) {
		t.Errorf("Pi session still exists: %v", err)
	}
}
