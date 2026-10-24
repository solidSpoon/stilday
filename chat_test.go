package main

import (
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"stilday/agent"
)

// A configured day renders the chat with its photo strip.
func TestChatPageShowsPhotos(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	src := t.TempDir()
	for i, c := range []color.RGBA{{200, 40, 40, 255}, {40, 200, 40, 255}, {40, 40, 200, 255}} {
		p := filepath.Join(src, "p"+string(rune('a'+i))+".jpg")
		makePhoto(t, p, c)
	}
	dir := a.cfg.workDayDir(a.day)
	start, end, err := a.cfg.photoWindow(a.day)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := preparePhotos(dir, photoOptions{
		Source: src, Day: a.day, Start: start, End: end, MaxPhotos: 50, SheetSize: 20,
	}, nil); err != nil {
		t.Fatal(err)
	}
	a.resetForDay()
	a.piPath = "/usr/local/bin/pi"
	a.piErr = ""

	tt := ui.NewTester(a.view, 1300, 820)
	tt.Frame()
	if !tt.HasText("大图") {
		t.Fatalf("the materials panel is missing: %q", tt.Texts())
	}
	if !tt.HasText("#001") {
		t.Errorf("photo thumbnails missing: %q", tt.Texts())
	}
	if !tt.HasText("还没有事件") {
		t.Errorf("the output placeholder is missing: %q", tt.Texts())
	}
}

// stilday_save_day's payload replaces the day's log.
func TestSaveDayEntries(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	a.chat.day = a.day.Format("2006-01-02")
	a.chat.dir = a.cfg.workDayDir(a.day)
	at := time.Date(a.day.Year(), a.day.Month(), a.day.Day(), 14, 5, 0, 0, a.day.Location()).Format(time.RFC3339)
	payload := json.RawMessage(fmt.Sprintf(`{"entries":[
		{"kind":"activity","summary":"在咖啡馆写了两个小时代码","occurred_at":%q,"data":{"place":"Blue Bottle"}},
		{"kind":"observation","summary":"完成了照片合集原型","occurred_at":%q},
		{"kind":"","summary":""}
	]}`, at, at))
	if n := a.saveDayEntries(payload); n != 2 {
		t.Fatalf("saved %d entries, want 2", n)
	}
	if got := len(a.entries); got != 2 {
		t.Fatalf("%d entries, want 2", got)
	}
	e := a.entries[0]
	if e.Kind != "activity" || e.Summary != "在咖啡馆写了两个小时代码" {
		t.Errorf("entry is %+v", e)
	}
	if e.Day != a.chat.day {
		t.Errorf("processing day is %q", e.Day)
	}
	if e.OccurredAt.Format("15:04") != "14:05" {
		t.Errorf("time is %s", e.OccurredAt.Format("15:04"))
	}

	// A second call is the new log, not an addition.
	at = time.Date(a.day.Year(), a.day.Month(), a.day.Day(), 9, 0, 0, 0, a.day.Location()).Format(time.RFC3339)
	again := json.RawMessage(fmt.Sprintf(
		`{"entries":[{"kind":"activity","summary":"早上在楼下买了咖啡","occurred_at":%q}]}`, at))
	if n := a.saveDayEntries(again); n != 1 {
		t.Fatalf("saved %d entries, want 1", n)
	}
	if got := len(a.entries); got != 1 || a.entries[0].Summary != "早上在楼下买了咖啡" {
		t.Fatalf("entries after the second save: %+v", a.entries)
	}
	if got := a.entries[0].OccurredAt.Format("15:04"); got != "09:00" {
		t.Fatalf("time after the second save: %s", got)
	}

	// The contact sheets show HH:MM; accept that shape too.
	third := json.RawMessage(`{"entries":[{"kind":"food","summary":"中午吃了面","occurred_at":"11:28"}]}`)
	if n := a.saveDayEntries(third); n != 1 {
		t.Fatalf("saved %d entries, want 1", n)
	}
	if got := a.entries[0].OccurredAt.Format("15:04"); got != "11:28" {
		t.Fatalf("HH:MM time: %s", got)
	}
	lines, err := os.ReadFile(a.store.Path(a.day))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(strings.TrimSpace(string(lines)), "\n") + 1; got != 1 {
		t.Fatalf("%d lines in the artifact, want 1", got)
	}
}

// Pi's event stream becomes chat rows, a transcript, and context lines.
func TestAgentEvents(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	a.chat.dir = a.cfg.workDayDir(a.day)
	if err := os.MkdirAll(a.chat.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	a.chat.day = a.day.Format("2006-01-02")
	a.chat.byCall = map[string]*chatMessage{}

	a.handleAgentEvent(a.chat, agent.Event{Type: "agent_start"})
	a.handleAgentEvent(a.chat, agent.Event{Type: "message_start", Message: agent.Message{Role: "assistant"}})
	a.handleAgentEvent(a.chat, agent.Event{
		Type:   "message_update",
		Update: agent.AssistantEvent{Type: "text_delta", Delta: "这一天在咖啡馆。"},
	})
	a.handleAgentEvent(a.chat, agent.Event{
		Type: "message_end",
		Message: agent.Message{Role: "assistant", Content: []agent.Content{
			{Type: "text", Text: "这一天在咖啡馆。"},
		}},
	})
	if len(a.chat.messages) != 1 || !a.chat.messages[0].Done {
		t.Fatalf("messages are %+v", a.chat.messages)
	}
	if got := a.chat.messages[0].Text; got != "这一天在咖啡馆。" {
		t.Errorf("text %q", got)
	}
	lines, err := os.ReadFile(filepath.Join(a.chat.dir, "chat.jsonl"))
	if err != nil || len(lines) == 0 {
		t.Fatalf("transcript: %v %q", err, lines)
	}

	a.handleAgentEvent(a.chat, agent.Event{Type: "tool_execution_start", ToolID: "t1", ToolName: "stilday_save_day"})
	a.handleAgentEvent(a.chat, agent.Event{
		Type: "tool_execution_end", ToolID: "t1", ToolName: "stilday_save_day",
		Result: agent.ToolResult{Details: json.RawMessage(`{"entries":[
			{"kind":"observation","summary":"在咖啡馆工作"},
			{"kind":"observation","summary":"下午去了超市"}
		]}`)},
	})
	if n := len(a.entries); n < 2 {
		t.Fatalf("%d entries after stilday_save_day", n)
	}
	last := a.entries[len(a.entries)-1]
	if last.Kind != "observation" || last.Summary != "下午去了超市" {
		t.Errorf("last entry is %+v", last)
	}
	if got := a.chat.messages[len(a.chat.messages)-1].ToolDetail; got != "保存 2 条" {
		t.Errorf("tool detail %q", got)
	}
}

// Without Pi, sending keeps the draft and explains what is missing.
func TestSendWithoutPi(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	t.Setenv("STILDAY_PI", filepath.Join(t.TempDir(), "no-pi"))
	a.piPath = ""
	a.piErr = "找不到 pi"
	a.sendChat("hello")
	if a.chat.err == "" {
		t.Fatal("sending without Pi reported no error")
	}
}

// A conversation stored before the current prompt was generated still
// opens on the current prompt, so the drawer shows today's kinds.
func TestSystemPromptDrawerIgnoresStaleCopy(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	a.chat.messages = []*chatMessage{{Role: "system", Text: "旧的系统提示", Done: true, TS: a.now}}
	if got := a.promptForDrawer(); !strings.Contains(got, "type StildaySaveDayArgs") {
		t.Fatalf("the drawer shows the stale copy: %q", got)
	}
}

func TestSystemPromptMessage(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	a.page = "day"
	a.openDay(a.day)
	a.chat.messages = []*chatMessage{{
		ID: 1, Role: "system", Done: true, TS: a.now,
		Text: promptBase() + "\n\n这是测试系统提示。",
	}}
	a.chat.nextID = 1
	tt := ui.NewTester(a.view, 1300, 820)
	tt.Frame()
	if err := tt.Click("系统提示"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("这是测试系统提示。") {
		t.Fatalf("the expanded prompt is missing: %q", tt.Texts())
	}
}
