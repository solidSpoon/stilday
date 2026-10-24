package main

import (
	"sort"
	"time"

	"stilday/internal/schedule"
)

// changedDaysSorted lists the days whose collectors changed, newest first.
func (a *app) changedDaysSorted() []string {
	days := make([]string, 0, len(a.calendarChanged))
	for key := range a.calendarChanged {
		days = append(days, key)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(days)))
	return days
}

// analysisDue is the next daily analysis time: today's AutoTime when it is
// still ahead, otherwise tomorrow's. A task registered at night therefore
// waits for the next morning's batch, and one registered in the morning
// runs at today's time.
func (a *app) analysisDue(now time.Time) time.Time {
	return specNext(now, schedule.Spec{Days: a.cfg.AutoDays, Time: a.cfg.AutoTime, Times: a.cfg.AutoTimes})
}

// syncAnalysisTasks publishes the changed days as scheduler tasks. A day
// with no task gets one waiting for the next daily analysis; a task whose
// day is no longer waiting is dropped. Cancelled and done tasks stay in the
// rail, and a running task is never dropped: it settles through FinishTask.
func (a *app) syncAnalysisTasks() {
	if s := a.scheduler(); s != nil {
		a.syncAnalysisTasksOn(s)
		a.requestRedraw()
	}
}

// requestRedraw asks for one frame so a task list that changed without
// input (a task finishing, a day appearing) is rebuilt.
func (a *app) requestRedraw() {
	if a.win != nil {
		a.win.Update(func() {})
	}
}

func (a *app) syncAnalysisTasksOn(s *schedule.Scheduler) {
	if s == nil {
		return
	}
	changed := map[string]bool{}
	for _, key := range a.changedDaysSorted() {
		changed[key] = true
	}
	for _, t := range s.Tasks() {
		if _, err := parseDayKey(t.Day); err != nil {
			s.Remove(t.ID)
			continue
		}
		switch t.State {
		case schedule.TaskCancelled, schedule.TaskRunning:
			continue
		case schedule.TaskDone:
			// A finished day is not in the list; drop the record unless its
			// material changed again (EnsureAnalysis reopens it then).
			if !changed[t.Day] {
				s.Remove(t.ID)
			}
			continue
		}
		if !changed[t.Day] {
			s.Remove(t.ID)
			continue
		}
		s.BindAnalysis(t.Day, a.analysisDue, a.runAnalysisTask)
	}
	for _, key := range a.changedDaysSorted() {
		// A new task has no start time yet: the daily analysis job gives
		// it one when it releases the day.
		s.EnsureAnalysis(key, func(time.Time) time.Time { return time.Time{} }, a.runAnalysisTask)
	}
}

// runAnalysisTask starts one day's analysis in its own conversation. It
// reports false while the slot is busy so the scheduler keeps the day
// waiting.
func (a *app) runAnalysisTask(day string) bool {
	return a.startAnalysisDay(day)
}

// analysisRunningDay is the day whose analysis holds the conversation slot,
// or "" when the slot is free or a user's turn holds it.
func (a *app) analysisRunningDay() string {
	if a.turn != nil && a.turn.analysis {
		return a.turn.day
	}
	return ""
}

// aiWorkingDay is the day the AI is replying to right now, manual turn or
// scheduled analysis: that day holds the one conversation slot.
func (a *app) aiWorkingDay() string {
	if a.turn != nil {
		return a.turn.day
	}
	return ""
}

// finishAnalysisTask settles the scheduler task of a day whose turn just
// ended. An empty error marks it done.
func (a *app) finishAnalysisTask(day, errText string) {
	if s := a.scheduler(); s != nil {
		s.FinishTask(schedule.AnalysisID(day), errText)
		a.requestRedraw()
	}
}

// startAllAnalysis makes every waiting day due now and starts the first
// one the slot allows.
func (a *app) startAllAnalysis() {
	if s := a.scheduler(); s != nil {
		s.StartAll()
	}
	a.pumpConversation()
	a.requestRedraw()
}

// cancelAnalysisTask cancels one day's analysis, stopping it first when it
// is the running one.
func (a *app) cancelAnalysisTask(day string) {
	if a.analysisRunningDay() == day {
		a.interruptAnalysis(day)
	}
	if s := a.scheduler(); s != nil {
		s.Cancel(schedule.AnalysisID(day))
	}
	a.pumpConversation()
	a.requestRedraw()
}

// cancelAllAnalysis cancels every waiting and running analysis.
func (a *app) cancelAllAnalysis() {
	if day := a.analysisRunningDay(); day != "" {
		a.interruptAnalysis(day)
	}
	if s := a.scheduler(); s != nil {
		s.CancelAll()
	}
	a.pumpConversation()
	a.requestRedraw()
}

// deleteAnalysisTask drops a cancelled day for good: its material counts as
// read, so it leaves the list and the calendar, and new material queues it
// again.
func (a *app) deleteAnalysisTask(day string) {
	a.markAnalyzed(day)
	if s := a.scheduler(); s != nil {
		s.Remove(schedule.AnalysisID(day))
	}
	a.refreshChangedDays()
	a.requestRedraw()
}

// clearCancelledTasks drops every cancelled day the same way.
func (a *app) clearCancelledTasks() {
	s := a.scheduler()
	if s == nil {
		return
	}
	for _, t := range s.Tasks() {
		if t.State == schedule.TaskCancelled {
			a.markAnalyzed(t.Day)
			s.Remove(t.ID)
		}
	}
	a.refreshChangedDays()
	a.requestRedraw()
}

// runAnalysisJob is the daily analysis job: at its time it lets the due
// tasks through. The tasks themselves are produced by the collectors when
// the day's material changes; the job is their deadline, not their maker.
func (a *app) runAnalysisJob() bool {
	s := a.scheduler()
	if s == nil {
		return true
	}
	// The job's result: the unscheduled days get their start time now.
	s.ReleaseAll()
	a.pumpConversation()
	return true
}

// startAnalysisNow runs the daily analysis job now: it sets the waiting
// tasks' deadline to now and lets them through.
func (a *app) startAnalysisNow() {
	s := a.scheduler()
	if s == nil {
		return
	}
	s.StartAll()
	s.Mark("analysis")
	a.pumpConversation()
}

// startAnalysisTask makes one waiting or failed day due now.
func (a *app) startAnalysisTask(day string) {
	s := a.scheduler()
	if s == nil {
		return
	}
	if s.Start(schedule.AnalysisID(day)) {
		a.syncAnalysisTasksOn(s)
		a.pumpConversation()
		a.requestRedraw()
	}
}

// restartAnalysisTask puts a cancelled or failed day back in the queue at
// the time it already had: overdue days run on the next tick, future ones
// wait for their time.
func (a *app) restartAnalysisTask(day string) {
	s := a.scheduler()
	if s == nil {
		return
	}
	if s.Restart(schedule.AnalysisID(day)) {
		a.syncAnalysisTasksOn(s)
		a.pumpConversation()
		a.requestRedraw()
	}
}

// pumpConversation gives the free slot to the first queued user message,
// and only when none waits, to the next due analysis day. This is what
// makes a user's message run before the next background day.
func (a *app) pumpConversation() {
	if a.turn != nil || a.collecting {
		return
	}
	if a.chat != nil && a.chat.preparing {
		return
	}
	for _, s := range a.chats {
		if s.preparing {
			return
		}
	}
	if len(a.pending) > 0 {
		pt := a.pending[0]
		a.pending = a.pending[1:]
		a.startQueuedTurn(pt)
		return
	}
	if s := a.scheduler(); s != nil {
		s.RunDue()
	}
}

// interruptAnalysis stops the running analysis and puts its day back in the
// waiting list without marking it done or cancelled. A collector run is
// about to replace the material it was reading.
func (a *app) interruptAnalysis(day string) {
	s := a.chats[day]
	if s == nil || a.turn != s {
		return
	}
	s.analysis = false
	a.turn = nil
	s.busy = false
	s.greetingPending = false
	a.finishStreaming(s)
	a.closeAgent(s)
	if sch := a.scheduler(); sch != nil {
		sch.Interrupt(schedule.AnalysisID(day), a.analysisDue(a.clockNow()))
	}
}
