package main

import (
	"path/filepath"
	"time"

	"stilday/internal/schedule"
)

// collectorJob is the scheduler's ID for one plugin.
func collectorJob(id string) string { return "collector:" + id }

// scheduler returns the daily scheduler, building it when the storage
// changes. Jobs read their times through the app, so edits apply at once.
// catchUpWindow is how late a missed job may still run on launch.
func (a *app) catchUpWindow() time.Duration {
	minutes := a.cfg.CatchUpMinutes
	if minutes <= 0 {
		minutes = 60
	}
	return time.Duration(minutes) * time.Minute
}

func (a *app) scheduler() *schedule.Scheduler {
	if !a.cfg.configured() {
		return nil
	}
	root := a.cfg.storageRoot()
	if a.sched != nil && a.schedRoot == root {
		return a.sched
	}
	s := schedule.New(filepath.Join(root, "schedule.json"), a.clockNow)
	s.Hold(a.cfg.AutoOff)
	s.SetCatchUp(a.catchUpWindow())
	for _, p := range a.collectorList {
		a.collectorHead(s, p)
	}
	a.analysisHead(s)
	a.sched, a.schedRoot = s, root
	// The tasks persisted for this storage root are bound to the app
	// again, and the days that changed while the app was away get theirs.
	a.syncAnalysisTasksOn(s)
	return s
}

// collectorHead ensures the collector's recurring task in the queue.
func (a *app) collectorHead(s *schedule.Scheduler, p collector) {
	sc := p.Schedule()
	if sc == nil {
		return
	}
	s.AddRecurring(collectorJob(p.ID()),
		schedule.Spec{Days: sc.Days, Time: sc.Time, Times: sc.Times},
		sc.Enabled,
		func() bool { return a.runScheduledCollector(p) })
}

// analysisHead ensures the daily analysis's recurring task in the queue.
func (a *app) analysisHead(s *schedule.Scheduler) {
	s.AddRecurring("analysis",
		schedule.Spec{Days: a.cfg.AutoDays, Time: a.cfg.AutoTime, Times: a.cfg.AutoTimes},
		!a.cfg.AutoOff,
		func() bool { return a.runAnalysisJob() })
}

// syncHeads pushes the edited specs into the scheduler's queue rows.
func (a *app) syncHeads() {
	s := a.scheduler()
	if s == nil {
		return
	}
	s.SetCatchUp(a.catchUpWindow())
	s.Hold(a.cfg.AutoOff)
	for _, p := range a.collectorList {
		a.collectorHead(s, p)
	}
	a.analysisHead(s)
}

// headNext is the occurrence a chain head would run next, for skipping it.
func (a *app) headNext(id string) time.Time {
	now := a.clockNow()
	if id == "analysis" {
		spec := schedule.Spec{Days: a.cfg.AutoDays, Time: a.cfg.AutoTime, Times: a.cfg.AutoTimes}
		if at, ok := specToday(now, spec); ok && now.Sub(at) <= a.catchUpWindow() {
			return at
		}
		return specNext(now, spec)
	}
	for _, p := range a.collectorList {
		if collectorJob(p.ID()) != id {
			continue
		}
		sc := p.Schedule()
		if sc == nil {
			return time.Time{}
		}
		spec := schedule.Spec{Days: sc.Days, Time: sc.Time, Times: sc.Times}
		if at, ok := specToday(now, spec); ok && now.Sub(at) <= a.catchUpWindow() {
			return at
		}
		return specNext(now, spec)
	}
	return time.Time{}
}

// cancelHead skips one occurrence of a recurring task: the cancelled row
// leaves the queue and a fresh one is registered for the next occurrence.
func (a *app) cancelHead(id string) {
	s := a.scheduler()
	if s == nil {
		return
	}
	// Skip the occurrence this head would run next; the row stays where it
	// is, so the list does not jump.
	if at := a.headNext(id); !at.IsZero() {
		s.SkipUntil(id, at)
	}
	a.requestRedraw()
}

// markCollectorRun tells the scheduler a plugin just ran by hand, so its
// daily time does not repeat it.
func (a *app) markCollectorRun(id string) {
	if s := a.scheduler(); s != nil {
		s.Mark(collectorJob(id))
	}
}

// runScheduledCollector starts one plugin at its time. false means the
// slot is busy and the scheduler should try again.
func (a *app) runScheduledCollector(p collector) bool {
	if !a.canStartCollector() {
		return false
	}
	now := a.clockNow()
	today := midnight(now)
	if a.syncRangeCollector(p.ID()) {
		return true
	}
	if !sameDay(a.day, today) || a.page != pageDay {
		// Open the day, then let the next tick run the collector on it.
		a.selectDay(today)
		return false
	}
	if a.chat == nil || !a.chat.loaded {
		return false
	}
	return a.startCollector(p)
}
