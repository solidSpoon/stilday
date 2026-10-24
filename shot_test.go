package main

import (
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"stilday/internal/artifact"
)

func TestShots(t *testing.T) {
	if os.Getenv("STILDAY_SHOTS") == "" {
		t.Skip("set STILDAY_SHOTS=1 to write screenshots")
	}
	a := newTestApp(t)

	root := t.TempDir()
	a.cfg = defaultConfig()
	a.cfg.StorageDir = root
	if err := a.cfg.prepareStorage(); err != nil {
		t.Fatal(err)
	}
	a.store = artifact.Open(a.cfg.artifactsRoot())
	a.day = midnight(time.Now())
	a.now = time.Now()
	a.page = "day"

	src := t.TempDir()
	for i, c := range []color.RGBA{
		{120, 150, 200, 255}, {200, 150, 90, 255}, {90, 170, 130, 255},
		{170, 100, 170, 255}, {200, 120, 90, 255}, {100, 140, 190, 255},
		{150, 160, 90, 255},
	} {
		p := filepath.Join(src, "p"+string(rune('a'+i))+".jpg")
		makePhoto(t, p, c)
	}
	start, end, err := a.cfg.photoWindow(a.day)
	if err != nil {
		t.Fatal(err)
	}
	dir := a.cfg.workDayDir(a.day)
	m, err := preparePhotos(dir, photoOptions{
		Source: src, Day: a.day, Start: start, End: end, MaxPhotos: 50, SheetSize: 30,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a.chat.loaded = true
	a.chat.day = a.day.Format("2006-01-02")
	a.chat.dir = dir
	a.chat.manifest = m
	a.chat.sheets = a.decodeSheets(m)
	a.chat.thumbs = a.decodeThumbs(m)
	a.chat.piPath = "/usr/local/bin/pi"
	a.chat.piErr = ""
	a.chat.sheetsSent = true
	a.chat.messages = []*chatMessage{
		{ID: 1, Role: "user", Text: "先看今天的照片，概述今天。", Done: true, Images: 1, TS: a.now.Add(-4 * time.Minute)},
		{ID: 2, Role: "tool", Tool: "day_overview", ToolStatus: "done", ToolDetail: "已查看", Done: true, TS: a.now.Add(-3 * time.Minute)},
		{ID: 3, Role: "assistant", Text: "今天大部分时间在 **咖啡馆写代码**，下午去了一趟超市。\n\n- 09:12 在家打开电脑\n- 11:40 到 Blue Bottle，一直写到 15:30\n- 17:20 超市采购，买了牛奶和水果\n\n已经把这些记到了今天的 JSONL 里。", Done: true, TS: a.now.Add(-2 * time.Minute)},
		{ID: 4, Role: "tool", Tool: "stilday_emit", ToolStatus: "done", ToolDetail: "记录 3 条", Done: true, TS: a.now.Add(-90 * time.Second)},
		{ID: 5, Role: "user", Text: "下午在咖啡馆主要做了什么？", Done: true, TS: a.now.Add(-60 * time.Second)},
	}
	a.chat.nextID = 5
	a.addEntry(Entry{OccurredAt: a.now, Day: a.day.Format("2006-01-02"), Kind: "place", Summary: "在 Blue Bottle 写了两个小时代码"})
	a.addEntry(Entry{OccurredAt: a.now.Add(-3 * time.Hour), Day: a.day.Format("2006-01-02"), Kind: "task", Summary: "完成了照片合集原型"})
	yesterday := a.day.AddDate(0, 0, -1)
	if err := a.store.Append(Entry{OccurredAt: yesterday.Add(20 * time.Hour), Day: yesterday.Format("2006-01-02"),
		Kind: "person", Summary: "和 Ada 吃了晚饭"}); err != nil {
		t.Fatal(err)
	}
	a.boardDirty = true

	tt := ui.NewTester(a.view, 1440, 900)
	tt.SetTitleBar(ui.TitleBar{Height: 46, Left: 76})
	save := func(name string) {
		f, err := os.Create("/tmp/stilday-" + name + ".png")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
	a.page = "day"
	// The first frame establishes the window and the theme; the second is
	// what a running window shows.
	tt.Frame()
	tt.Frame()
	save("day-light")
	// The calendar's narrow state: the content gets the room.
	a.sidebarWide = false
	tt.Frame()
	save("sidebar-narrow")
	a.sidebarWide = true
	tt.Frame()
	// The composer with a draft: the send button takes the accent color.
	a.chat.input = "下午在咖啡馆主要做了什么？"
	tt.Frame()
	save("day-typed")
	a.chat.input = ""
	tt.Frame()
	// The window on macOS shows the system's sidebar material; the
	// tester's stand-in checks that the labels read over it.
	tt.SetVibrancy(true)
	save("day-vibrancy")
	tt.SetVibrancy(false)
	a.page = "collectors"
	tt.Frame()
	save("collectors-light")
	a.page = "board"
	a.boardDirty = true
	tt.Frame()
	save("board-light")
	a.openSettings()
	tt.Frame()
	save("settings-light")
	tt.Scroll(800, 500, 0, 420)
	tt.Frame()
	save("settings-scrolled")
	tt.Scroll(800, 500, 0, -420)
	tt.Frame()
	a.page = "day"
	tt.SetDark(true)
	tt.Frame()
	save("day-dark")
	a.openSettings()
	tt.Frame()
	save("settings-dark")
	a.page = "board"
	a.boardDirty = true
	tt.Frame()
	save("board-dark")
	tt.SetDark(false)
	a.page = "day"
	tt.SetSize(1200, 760)
	tt.Frame()
	save("day-narrow")

	// A day without a conversation: the thread's empty state above the
	// composer, which is its last row.
	tt.SetSize(1440, 900)
	a.chat.messages = nil
	a.entries = nil
	tt.Frame()
	save("day-empty")
	// The collectors page with a background analysis: the queue, a
	// cancelled day, and the status bar in the sidebar.
	a.chat.messages = nil
	a.page = "collectors"
	a.calendarChanged["2026-10-20"] = true
	a.calendarChanged["2026-10-19"] = true
	a.calendarChanged["2026-10-18"] = true
	a.analysis.Queue = []string{"2026-10-20", "2026-10-19"}
	a.analysis.Cancelled["2026-10-18"] = time.Now()
	tt.Frame()
	save("collectors-tasks")
	a.analysis.Running, a.analysis.Current = true, "2026-10-20"
	a.analysis.Queue = []string{"2026-10-19"}
	delete(a.calendarChanged, "2026-10-18")
	delete(a.analysis.Cancelled, "2026-10-18")
	tt.Frame()
	save("collectors-running")
}
