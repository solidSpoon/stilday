package main

import (
	"sort"

	"stilday/internal/analysis"
)

// wireAnalysis connects the queue to the app: the changed days it reads,
// the day switch it performs, the fingerprint it records, and the agent it
// stops.
func (a *app) wireAnalysis() {
	a.analysis = analysis.New(analysis.Hooks{
		Pending:      a.changedDaysSorted,
		StartDay:     a.startAnalysisDay,
		MarkAnalyzed: a.markAnalyzed,
		Stop:         a.stopAgent,
	})
}

// changedDaysSorted lists the days whose collectors changed, newest first.
func (a *app) changedDaysSorted() []string {
	days := make([]string, 0, len(a.calendarChanged))
	for key := range a.calendarChanged {
		days = append(days, key)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(days)))
	return days
}

// startAnalysisDay switches the app to a queued day and starts its
// analysis, reporting whether an analysis actually began.
func (a *app) startAnalysisDay(key string) bool {
	day, err := parseDayKey(key)
	if err != nil {
		return true // next() already skipped malformed keys
	}
	a.selectDay(day)
	a.startAnalysis()
	return a.chat.busy || a.chat.preparing || a.chat.greetingPending
}

// finishAnalysisTurn records the day the model just read, if the turn
// succeeded.
func (a *app) finishAnalysisTurn() {
	a.analysis.Finish(a.chat.err == "")
}
