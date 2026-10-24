package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"stilday/agent"
	"stilday/internal/schedule"
)

// startAnalysisDay analyzes one day in that day's own conversation,
// without changing a.day or a.page. It returns true when the turn began;
// false leaves the day waiting for the scheduler to retry.
func (a *app) startAnalysisDay(key string) bool {
	if !a.chatIdle() || len(a.pending) > 0 {
		return false
	}
	if !a.cfg.configured() {
		return false
	}
	day, err := parseDayKey(key)
	if err != nil {
		return false
	}
	// Resolve everything the child needs before the day's old analysis is
	// cleared, as the background run always did.
	now := a.clockNow()
	deferToNextDay := func() bool {
		// No Pi or no key is not a failed day: keep it waiting for the
		// next daily analysis time instead of checking every frame.
		if sch := a.scheduler(); sch != nil {
			sch.Interrupt(schedule.AnalysisID(key), a.analysisDue(now))
		}
		return false
	}
	st, err := a.prepareAgent()
	if err != nil {
		return deferToNextDay()
	}
	// The window checks the keychain before the first turn; tests have no
	// keychain and no window, so they go straight to the Pi error.
	if a.win != nil && !a.hasProviderKey() {
		return deferToNextDay()
	}
	s := a.dayState(day)
	if !a.claimTurn(s, true) {
		return false
	}
	// A re-analysis rewrites the day: old entries, transcript and Pi
	// session go first, and a page showing this day refreshes with it.
	a.beginAnalysisSession(s)
	if err := a.startAgentWith(s, st); err != nil {
		s.analysis = false
		a.turn = nil
		s.busy = false
		if sch := a.scheduler(); sch != nil {
			sch.FinishTask(schedule.AnalysisID(key), err.Error())
		}
		return false
	}
	m := s.manifest
	images := sheetImages(s.dir, m)
	text := "请分析这一天的照片，把值得记住的事记下来。"
	if m != nil && len(m.Sheets) > 0 {
		text = fmt.Sprintf("（附件是这一天的照片合集，编号 #1–%d。）\n\n%s", len(m.Photos), text)
	}
	a.addMessage(s, &chatMessage{Role: "user", Text: text, Images: len(images), Done: true, TS: time.Now()})
	a.sendPrompt(s, text, images)
	return true
}

// sheetFiles returns the day's sheet images, in order.
func sheetFiles(dir string, m *manifest) [][]byte {
	if m == nil {
		return nil
	}
	var out [][]byte
	for _, sh := range m.Sheets {
		b, err := os.ReadFile(filepath.Join(dir, sh.File))
		if err == nil && len(b) > 0 {
			out = append(out, b)
		}
	}
	return out
}

// sheetImages wraps the day's sheets for the Pi prompt.
func sheetImages(dir string, m *manifest) []agent.Image {
	files := sheetFiles(dir, m)
	out := make([]agent.Image, 0, len(files))
	for _, data := range files {
		out = append(out, agent.NewImage(data, "image/jpeg"))
	}
	return out
}
