package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"stilday/agent"
)

func (a *app) systemPrompt() string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(systemPromptBase))
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "今天是 %s。\n", a.chat.day)
	fmt.Fprintf(&b, "当天的文件夹是 %s。\n\n", a.chat.dir)
	b.WriteString("采集器（按执行顺序）：\n")
	for _, p := range a.collectorList {
		fmt.Fprintf(&b, "- %s（%s），文件夹 %s/：%s\n", p.ID(), p.Name(), p.Folder(), p.Description())
	}
	b.WriteString("每个采集器把原始材料放在自己的文件夹里；manifest.json 列出每个采集器及其文件夹。照片采集器把 1600px JPEG 写入 previews/，界面用的小 JPEG 写入 thumbs/，编号合集写入 sheets/；不保留原图。\n")
	if m := a.chat.manifest; m != nil && len(m.Photos) > 0 {
		fmt.Fprintf(&b, "当天有 %d 张编号照片，分布在 %d 张合集里。合集已附在当天的第一条消息里；需要再看时可以调用 day_overview。\n", len(m.Photos), len(m.Sheets))
	} else {
		b.WriteString("今天还没有照片；先根据对话回答，如果需要照片就说明。\n")
	}
	return b.String()
}

type emittedEntry struct {
	Kind       string `json:"kind"`
	Summary    string `json:"summary"`
	Photo      *int   `json:"photo"`
	OccurredAt string `json:"occurred_at"`
	TS         string `json:"ts"` // old payloads; read-only
}

// emitEntries maps the model's stilday_emit payload into the day's JSONL
// store. The mapping is Stilday's own: the model only says what it saw.
func (a *app) emitEntries(details json.RawMessage) int {
	if len(details) == 0 {
		return 0
	}
	var payload struct {
		Entries []emittedEntry `json:"entries"`
	}
	if err := json.Unmarshal(details, &payload); err != nil {
		a.chat.err = "stilday_emit: " + err.Error()
		return 0
	}
	n := 0
	for _, e := range payload.Entries {
		summary := strings.TrimSpace(e.Summary)
		if summary == "" {
			continue
		}
		kind := strings.TrimSpace(e.Kind)
		if kind == "" {
			kind = "observation"
		}
		ts := time.Now()
		stamp := e.OccurredAt
		if stamp == "" {
			stamp = e.TS
		}
		if stamp != "" {
			if t, err := time.Parse(time.RFC3339, stamp); err == nil {
				ts = t
			}
		}
		if e.Photo != nil && a.chat.manifest != nil {
			if p := a.chat.manifest.photo(*e.Photo); p != nil {
				if t, err := time.Parse(time.RFC3339, p.CapturedAt); err == nil {
					ts = t
				}
			}
		}
		a.addEntry(Entry{OccurredAt: ts, Day: a.chat.day, Kind: kind, Summary: summary})
		n++
	}
	return n
}

func toolDetail(ev agent.Event) string {
	switch ev.ToolName {
	case "day_overview":
		return "已查看"
	case "stilday_emit":
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
