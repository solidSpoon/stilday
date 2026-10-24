package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"stilday/agent"
)

func (a *app) systemPrompt() string {
	return a.systemPromptFor(a.chat.day, a.chat.dir, a.chat.manifest)
}

// systemPromptFor builds the prompt for a day's files, whether the day is
// on screen or analyzed in the background.
func (a *app) systemPromptFor(day, dir string, m *manifest) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(promptBase()))
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "今天是 %s。\n", day)
	fmt.Fprintf(&b, "当天的文件夹是 %s。\n\n", dir)
	b.WriteString("采集器（按执行顺序）：\n")
	for _, p := range a.collectorList {
		fmt.Fprintf(&b, "- %s（%s），文件夹 %s/：%s\n", p.ID(), p.Name(), p.Folder(), p.Description())
	}
	b.WriteString("每个采集器把原始材料放在自己的文件夹里；manifest.json 列出每个采集器及其文件夹。照片采集器把 1600px JPEG 写入 previews/，界面用的小 JPEG 写入 thumbs/，编号合集写入 sheets/；不保留原图。\n")
	if m != nil && len(m.Photos) > 0 {
		fmt.Fprintf(&b, "当天有 %d 张编号照片，分布在 %d 张合集里。合集已附在当天的第一条消息里；需要再看时可以调用 day_overview。\n", len(m.Photos), len(m.Sheets))
	} else {
		b.WriteString("今天还没有照片；先根据对话回答，如果需要照片就说明。\n")
	}
	return b.String()
}

type dayLogEntry struct {
	Kind       string `json:"kind"`
	Summary    string `json:"summary"`
	OccurredAt string `json:"occurred_at"`
	TS         string `json:"ts"` // old payloads; read-only
}

// saveDayEntries saves the model's log for the day. The payload is the
// complete list: it replaces the day's entries, so a later chat can fix the
// log by sending the whole list again.
func (a *app) saveDayEntries(details json.RawMessage) int {
	return a.saveDayEntriesFor(a.chat.day, details)
}

// saveDayEntriesFor applies a stilday_save_day call to a day's log,
// whether that day is on screen or analyzed in the background.
func (a *app) saveDayEntriesFor(key string, details json.RawMessage) int {
	if len(details) == 0 {
		return 0
	}
	var payload struct {
		Entries []dayLogEntry `json:"entries"`
	}
	if err := json.Unmarshal(details, &payload); err != nil {
		return 0
	}
	day, err := time.Parse("2006-01-02", key)
	if err != nil {
		return 0
	}
	entries := make([]Entry, 0, len(payload.Entries))
	for _, e := range payload.Entries {
		summary := strings.TrimSpace(e.Summary)
		if summary == "" {
			continue
		}
		kind := strings.TrimSpace(e.Kind)
		if kind == "" {
			kind = "observation"
		}
		stamp := e.OccurredAt
		if stamp == "" {
			stamp = e.TS
		}
		ts, ok := parseStamp(stamp, day)
		if !ok {
			ts = time.Now()
		}
		entries = append(entries, Entry{OccurredAt: ts, Day: key, Kind: kind, Summary: summary})
	}
	if len(entries) == 0 {
		// An empty list would wipe the day; keep what is there.
		return 0
	}
	if err := a.store.ClearDay(day); err != nil {
		return 0
	}
	selected := sameDay(day, a.day)
	if selected {
		// The page shows this day: replace its in-memory timeline too, so
		// the events appear as the model writes them.
		a.entries = nil
		a.seq = 0
		a.outputFocus = 0
		for _, e := range entries {
			a.addEntry(e)
		}
	} else {
		// Another day: write it to the year file without touching the
		// selected day's timeline.
		for _, e := range entries {
			a.appendEntry(e)
		}
	}
	a.dataDays[key] = true
	a.boardDirty = true
	return len(entries)
}

func toolDetail(ev agent.Event) string {
	switch ev.ToolName {
	case "day_overview":
		return "已查看"
	case "stilday_save_day":
		if !ev.IsError {
			var p struct {
				Entries []json.RawMessage `json:"entries"`
			}
			if json.Unmarshal(ev.Result.Details, &p) == nil {
				return fmt.Sprintf("%d 条", len(p.Entries))
			}
		}
	}
	if ev.IsError {
		if len(ev.Result.Content) > 0 && ev.Result.Content[0].Text != "" {
			s := ev.Result.Content[0].Text
			if len(s) > 80 {
				s = s[:80] + "…"
			}
			return s
		}
	}
	return ""
}

// parseStamp reads occurred_at: RFC 3339, or the shapes the contact sheets
// use (a local time, or a local date and time without an offset).
func parseStamp(stamp string, day time.Time) (time.Time, bool) {
	stamp = strings.TrimSpace(stamp)
	if stamp == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, stamp); err == nil {
		return t, true
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04"} {
		if t, err := time.ParseInLocation(layout, stamp, time.Local); err == nil {
			return t, true
		}
	}
	for _, layout := range []string{"15:04:05", "15:04"} {
		if t, err := time.Parse(layout, stamp); err == nil {
			return time.Date(day.Year(), day.Month(), day.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.Local), true
		}
	}
	return time.Time{}, false
}
