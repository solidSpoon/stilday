package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"stilday/internal/schedule"
)

// fakePi is a Pi child that answers one prompt with a text reply and one
// saved-day tool call. delay makes its turn long enough to observe.
func fakePi(t *testing.T, delay string) string {
	t.Helper()
	script := `#!/bin/sh
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  case "$line" in
    *'"type":"prompt"'*)
      printf '%s\n' '{"type":"agent_start"}'
      sleep ` + delay + `
      printf '%s\n' '{"type":"message_start","message":{"role":"assistant","content":[]}}'
      printf '%s\n' '{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"分析完成"}}'
      printf '%s\n' '{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"分析完成"}]}}'
      printf '%s\n' '{"type":"tool_execution_start","toolCallId":"t1","toolName":"stilday_save_day","args":{}}'
      printf '%s\n' '{"type":"tool_execution_end","toolCallId":"t1","toolName":"stilday_save_day","result":{"content":[{"type":"text","text":"ok"}],"details":{"entries":[{"kind":"observation","summary":"从照片里记下的事"}]}}}'
      printf '%s\n' '{"type":"agent_settled","aborted":false}'
      printf '{"type":"response","id":"%s","command":"prompt","success":true,"data":{"disposition":"started"}}\n' "$id"
      ;;
    *'"type":"abort"'*)
      printf '{"type":"response","id":"%s","command":"abort","success":true}\n' "$id"
      ;;
  esac
done
`
	path := filepath.Join(t.TempDir(), "pi")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// waitFor polls until cond is true, or fails the test.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// markDayChanged writes one collector fingerprint for a day and refreshes
// the pending set.
func markDayChanged(t *testing.T, a *app, day time.Time, fingerprint string) {
	t.Helper()
	dir := a.cfg.workDayDir(day)
	state := loadCollectorDay(dir)
	if state.Items == nil {
		state.Items = map[string]collectorRun{}
	}
	run := state.Items["photos"]
	run.Status = "done"
	run.Fingerprint = fingerprint
	state.Items["photos"] = run
	if err := saveCollectorDay(dir, state); err != nil {
		t.Fatal(err)
	}
	a.refreshChangedDays()
}

// An analysis runs in the analyzed day's conversation: the view stays on
// whatever day the user chose, and the day's transcript, entries and
// fingerprints are written in its own folder.
func TestAnalysisRunsInItsOwnDayConversation(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	other := a.day.AddDate(0, 0, -1)
	a.now = time.Date(a.day.Year(), a.day.Month(), a.day.Day(), 9, 0, 0, 0, time.Local)
	a.cfg.AutoTime = "10:00"
	markDayChanged(t, a, other, "sha256:one")

	a.piPath = fakePi(t, "0.05")
	a.piErr = ""
	page, selected := a.page, a.day
	a.now = a.now.Add(time.Hour + time.Minute)
	a.scheduler().Tick()

	id := schedule.AnalysisID(dayKey(other))
	waitFor(t, "the analysis task to finish", func() bool {
		task, ok := a.scheduler().Task(id)
		return ok && task.State == schedule.TaskDone
	})
	if a.page != page || !a.day.Equal(selected) {
		t.Fatalf("analysis moved the view: page=%v day=%v", a.page, a.day)
	}
	if a.chat == nil || a.chat.day != dayKey(selected) {
		t.Fatalf("the selected conversation changed: %+v", a.chat)
	}
	entries, err := a.store.Load(other)
	if err != nil || len(entries) != 1 || entries[0].Summary != "从照片里记下的事" {
		t.Fatalf("analyzed entries are %+v, %v", entries, err)
	}
	if len(a.entries) != 0 {
		t.Fatalf("another day's entries leaked into the view: %+v", a.entries)
	}
	s := a.chats[dayKey(other)]
	if s == nil {
		t.Fatal("the analyzed day has no conversation")
	}
	found := false
	for _, m := range s.messages {
		if m.Role == "assistant" && m.Text == "分析完成" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the analyzed day's transcript is %+v", s.messages)
	}
	if _, err := os.Stat(filepath.Join(a.cfg.workDayDir(other), "chat.jsonl")); err != nil {
		t.Fatal("the transcript was not written:", err)
	}
	if state := loadCollectorDay(a.cfg.workDayDir(other)); state.Items["photos"].AnalyzedFingerprint != "sha256:one" {
		t.Fatalf("the fingerprint was not recorded: %+v", state.Items["photos"])
	}
}

// When the page shows the day being analyzed, the same conversation the
// page renders fills with the turn as it happens.
func TestAnalysisUpdatesTheShownDayLive(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	day := a.day.AddDate(0, 0, -1)
	a.selectDay(day)
	a.now = time.Date(day.Year(), day.Month(), day.Day(), 9, 0, 0, 0, time.Local)
	a.cfg.AutoTime = "10:00"
	markDayChanged(t, a, day, "sha256:shown")
	a.piPath = fakePi(t, "0.3")
	a.piErr = ""
	a.now = a.now.Add(time.Hour + time.Minute)
	a.scheduler().Tick()

	waitFor(t, "the shown day's analysis to run", func() bool {
		return a.analysisRunningDay() == dayKey(day)
	})
	if a.chat == nil || a.chat.day != dayKey(day) {
		t.Fatalf("the view left the analyzed day: %+v", a.chat)
	}
	if len(a.chat.messages) == 0 || !strings.Contains(a.chat.messages[0].Text, "请分析这一天") {
		t.Fatalf("the live conversation is %+v", a.chat.messages)
	}
	waitFor(t, "the analysis to finish", func() bool {
		task, ok := a.scheduler().Task(schedule.AnalysisID(dayKey(day)))
		return ok && task.State == schedule.TaskDone
	})
	stored, _ := a.store.Load(day)
	if got := len(a.entries); got != 1 || a.entries[0].Summary != "从照片里记下的事" {
		t.Fatalf("the shown day's entries are %+v (chat day %q, a.day %s, stored %+v, messages %+v, err %q)", a.entries, a.chat.day, a.day.Format("2006-01-02"), stored, a.chats[dayKey(day)].messages, a.chats[dayKey(day)].err)
	}
}

// A message sent while the analysis runs is accepted, shown in that day's
// conversation, and its turn runs before the next analysis day.
func TestQueuedUserTurnRunsBeforeNextAnalysis(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	first := a.day.AddDate(0, 0, -1)
	second := a.day.AddDate(0, 0, -2)
	a.now = time.Date(a.day.Year(), a.day.Month(), a.day.Day(), 9, 0, 0, 0, time.Local)
	a.cfg.AutoTime = "10:00"
	markDayChanged(t, a, first, "sha256:first")
	markDayChanged(t, a, second, "sha256:second")
	// Newest first: the first day's task is the one that starts.
	if got := a.scheduler().Tasks(); len(got) != 2 || got[0].Day != dayKey(first) {
		t.Fatalf("task order is %+v", got)
	}
	a.piPath = fakePi(t, "0.3")
	a.piErr = ""
	a.now = a.now.Add(time.Hour + time.Minute)
	a.scheduler().Tick()

	waitFor(t, "the first analysis to run", func() bool {
		return a.analysisRunningDay() == dayKey(first)
	})
	state := a.chats[dayKey(first)]
	a.sendChatOn(state, "再加上这件事")
	if len(a.pending) != 1 {
		t.Fatalf("%d queued turns, want 1", len(a.pending))
	}
	if n := len(state.messages); n == 0 || state.messages[n-1].Text != "再加上这件事" {
		t.Fatalf("the queued message is not shown: %+v", state.messages)
	}

	firstID := schedule.AnalysisID(dayKey(first))
	secondID := schedule.AnalysisID(dayKey(second))
	waitFor(t, "the first analysis to finish", func() bool {
		task, ok := a.scheduler().Task(firstID)
		return ok && task.State == schedule.TaskDone
	})
	// The user's turn holds the slot before the next day's analysis.
	waitFor(t, "the queued turn to start", func() bool {
		return a.turn == state
	})
	if task, _ := a.scheduler().Task(secondID); task.State != schedule.TaskPending {
		t.Fatalf("the next analysis started before the user's turn: %+v", task)
	}
	waitFor(t, "the queued turn to finish", func() bool {
		return a.turn == nil
	})
	waitFor(t, "the second analysis to finish", func() bool {
		task, ok := a.scheduler().Task(secondID)
		return ok && task.State == schedule.TaskDone
	})
	found := false
	for _, m := range state.messages {
		if m.Role == "assistant" && m.Text == "分析完成" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the queued turn produced no reply: %+v", state.messages)
	}
}

// Switching days keeps each day's conversation, and does not stop a turn
// running on the day left behind.
func TestSwitchingDaysKeepsTheRunningConversation(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	today := a.day
	yesterday := a.day.AddDate(0, 0, -1)
	a.piPath = fakePi(t, "0.3")
	a.piErr = ""
	a.openDay(today)
	todayState := a.chat
	a.sendChatOn(todayState, "今天先记一笔")
	waitFor(t, "the turn to start", func() bool { return todayState.busy })

	a.selectDay(yesterday)
	if a.chat == nil || a.chat.day != dayKey(yesterday) {
		t.Fatalf("the view did not switch: %+v", a.chat)
	}
	if a.turn != todayState || todayState.client == nil {
		t.Fatal("switching days stopped the running conversation")
	}
	a.sendChatOn(a.chat, "昨天也记一笔")
	if a.chat.day != dayKey(yesterday) || len(a.chat.messages) != 1 {
		t.Fatalf("yesterday's conversation is %+v", a.chat.messages)
	}

	waitFor(t, "today's turn to finish", func() bool { return a.turn == nil })
	a.selectDay(today)
	if a.chat != todayState {
		t.Fatal("switching back did not restore today's conversation")
	}
	found := false
	for _, m := range a.chat.messages {
		if m.Role == "assistant" && m.Text == "分析完成" {
			found = true
		}
	}
	if !found {
		t.Fatalf("today's live conversation is %+v", a.chat.messages)
	}
	yesterdayState := a.chats[dayKey(yesterday)]
	if yesterdayState == nil || len(yesterdayState.messages) == 0 {
		t.Fatal("yesterday's queued message was lost")
	}
	if yesterdayState.messages[0].Text != "昨天也记一笔" {
		t.Fatalf("yesterday's queued message is %+v", yesterdayState.messages)
	}
}

// A collector run interrupts the running analysis without cancelling its
// day: the task waits for the next daily time.
func TestCollectorInterruptsTheAnalysisWithoutCancelling(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	day := a.day.AddDate(0, 0, -1)
	a.now = time.Date(a.day.Year(), a.day.Month(), a.day.Day(), 9, 0, 0, 0, time.Local)
	a.cfg.AutoTime = "10:00"
	markDayChanged(t, a, day, "sha256:interrupt")
	a.piPath = fakePi(t, "0.4")
	a.piErr = ""
	a.now = a.now.Add(time.Hour + time.Minute)
	a.scheduler().Tick()
	waitFor(t, "the analysis to run", func() bool { return a.analysisRunningDay() == dayKey(day) })

	if !a.takeConversationSlot() {
		t.Fatal("a collector could not take the slot from the analysis")
	}
	if a.analysisRunningDay() != "" || a.turn != nil {
		t.Fatal("the interrupted analysis kept the slot")
	}
	task, ok := a.scheduler().Task(schedule.AnalysisID(dayKey(day)))
	if !ok || task.State != schedule.TaskPending {
		t.Fatalf("the interrupted task is %+v, %v", task, ok)
	}
	if !task.Due.After(a.now) {
		t.Fatalf("the interrupted task is due at %s, not the next daily time", task.Due)
	}
}

// The collector rail shows the day the scheduler is analyzing and the day
// waiting behind it, with the same wording as before.
func TestCollectorRailShowsTheRunningAnalysis(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	first := a.day.AddDate(0, 0, -1)
	second := a.day.AddDate(0, 0, -2)
	a.now = time.Now()
	a.cfg.AutoTime = "10:00"
	markDayChanged(t, a, first, "sha256:rail-first")
	markDayChanged(t, a, second, "sha256:rail-second")
	a.piPath = fakePi(t, "0.6")
	a.piErr = ""
	a.startAllAnalysis()

	waitFor(t, "the first analysis to run", func() bool {
		return a.analysisRunningDay() == dayKey(first)
	})
	a.page = pageCollectors
	tt := ui.NewTester(a.view, 1440, 900)
	tt.Frame()
	if !tt.HasText("就绪") {
		t.Fatalf("the rail does not show the ready day: %q", tt.Texts())
	}
	if !tt.HasText(dayKey(first)) || !tt.HasText(dayKey(second)) {
		t.Fatalf("the rail is missing a day: %q", tt.Texts())
	}
	waitFor(t, "both analyses to finish", func() bool {
		// A finished task leaves the list and the state: missing is done.
		if t, ok := a.scheduler().Task(schedule.AnalysisID(dayKey(first))); ok && t.State != schedule.TaskDone {
			return false
		}
		if t, ok := a.scheduler().Task(schedule.AnalysisID(dayKey(second))); ok && t.State != schedule.TaskDone {
			return false
		}
		return true
	})
}

// Analysis task state lives in the scheduler's own file under the storage
// root, never in the day folders.
func TestAnalysisTaskStateLivesInScheduleFile(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	day := a.day.AddDate(0, 0, -1)
	a.now = time.Date(a.day.Year(), a.day.Month(), a.day.Day(), 9, 0, 0, 0, time.Local)
	a.cfg.AutoTime = "10:00"
	markDayChanged(t, a, day, "sha256:state")

	raw, err := os.ReadFile(filepath.Join(a.cfg.storageRoot(), "schedule.json"))
	if err != nil {
		t.Fatal("the scheduler file was not written:", err)
	}
	if !strings.Contains(string(raw), `"analysis:`+dayKey(day)+`"`) {
		t.Fatalf("the task is not in the scheduler file: %s", raw)
	}
	if !strings.Contains(string(raw), `"state": "pending"`) {
		t.Fatalf("the task state is not persisted: %s", raw)
	}
	entries, err := os.ReadDir(a.cfg.workDayDir(day))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := strings.ToLower(e.Name())
		if strings.Contains(name, "schedule") || strings.Contains(name, "task") {
			t.Fatalf("scheduling state leaked into the day folder: %s", e.Name())
		}
	}
}
