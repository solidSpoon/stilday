package schedule

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func at(hhmm string) time.Time {
	t, err := time.Parse("2006-01-02 15:04", "2026-10-24 "+hhmm)
	if err != nil {
		panic(err)
	}
	return t
}

func TestSchedulerRunsOnceAfterItsTime(t *testing.T) {
	now := at("09:00")
	s := New("", func() time.Time { return now })
	runs := 0
	s.AddRecurring("job", Spec{Time: "10:00"}, true, func() bool { runs++; return true })
	s.Tick()
	if runs != 0 {
		t.Fatalf("ran before its time: %d", runs)
	}
	now = at("10:01")
	s.Tick()
	s.Tick()
	if runs != 1 {
		t.Fatalf("ran %d times in one day", runs)
	}
	if last, ok := s.Last("job"); !ok || !last.Equal(now) {
		t.Fatalf("last run is %v, %v", last, ok)
	}
	// The next day it runs again.
	now = at("10:01").AddDate(0, 0, 1)
	s.Tick()
	if runs != 2 {
		t.Fatalf("did not run the next day: %d", runs)
	}
}

func TestSchedulerRetriesAJobThatCouldNotStart(t *testing.T) {
	now := at("10:01")
	s := New("", func() time.Time { return now })
	busy := true
	runs := 0
	s.AddRecurring("job", Spec{Time: "10:00"}, true, func() bool {
		if busy {
			return false
		}
		runs++
		return true
	})
	s.Tick()
	if runs != 0 {
		t.Fatalf("ran while busy: %d", runs)
	}
	if _, ok := s.Last("job"); ok {
		t.Fatal("a job that did not start was recorded as run")
	}
	busy = false
	s.Tick()
	if runs != 1 {
		t.Fatalf("did not retry: %d", runs)
	}
}

func TestSchedulerKeepsItsLastRunAcrossLaunches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schedule.json")
	now := at("10:01")
	s := New(path, func() time.Time { return now })
	runs := 0
	spec := Spec{Time: "10:00"}
	run := func() bool { runs++; return true }
	s.AddRecurring("job", spec, true, run)
	s.Tick()
	if runs != 1 {
		t.Fatalf("ran %d times", runs)
	}
	// A new process, same day, same file: it must not run again.
	s2 := New(path, func() time.Time { return now })
	s2.AddRecurring("job", spec, true, run)
	s2.Tick()
	if runs != 1 {
		t.Fatalf("a new scheduler reran the job: %d", runs)
	}
}

func TestSchedulerSkipsDisabledJobs(t *testing.T) {
	now := at("23:00")
	s := New("", func() time.Time { return now })
	runs := 0
	s.AddRecurring("job", Spec{Time: "10:00"}, false, func() bool { runs++; return true })
	s.Tick()
	if runs != 0 {
		t.Fatalf("a disabled job ran: %d", runs)
	}
}

// analysisFixture is one day's task with a run that records itself.
type analysisFixture struct {
	started []string
	refuse  bool
}

func (f *analysisFixture) due(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(), 10, 0, 0, 0, now.Location())
}

func (f *analysisFixture) run(day string) bool {
	if f.refuse {
		return false
	}
	f.started = append(f.started, day)
	return true
}

func newAnalysisScheduler(t *testing.T, now *time.Time, f *analysisFixture) (*Scheduler, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "schedule.json")
	s := New(path, func() time.Time { return *now })
	s.EnsureAnalysis("2026-10-20", f.due, f.run)
	s.EnsureAnalysis("2026-10-19", f.due, f.run)
	return s, path
}

func TestAnalysisTaskRunsAtItsTimeAndFinishes(t *testing.T) {
	now := at("09:00")
	f := &analysisFixture{}
	s, _ := newAnalysisScheduler(t, &now, f)
	s.Tick()
	if len(f.started) != 0 {
		t.Fatalf("ran before its time: %v", f.started)
	}
	now = at("10:01")
	s.Tick()
	// One conversation at a time: the first day runs, the second waits.
	if len(f.started) != 1 || f.started[0] != "2026-10-20" {
		t.Fatalf("started %v", f.started)
	}
	running, ok := s.RunningTask()
	if !ok || running.Day != "2026-10-20" {
		t.Fatalf("running task is %+v, %v", running, ok)
	}
	s.Tick()
	if len(f.started) != 1 {
		t.Fatalf("a second day started while one ran: %v", f.started)
	}
	if !s.FinishTask(AnalysisID("2026-10-20"), "") {
		t.Fatal("finishing the running task failed")
	}
	task, _ := s.Task(AnalysisID("2026-10-20"))
	if task.State != TaskDone {
		t.Fatalf("finished task is %q", task.State)
	}
	if n := s.CountPending(); n != 1 {
		t.Fatalf("%d pending after finishing, want 1", n)
	}
	s.Tick()
	if len(f.started) != 2 || f.started[1] != "2026-10-19" {
		t.Fatalf("the next day did not start: %v", f.started)
	}
}

func TestAnalysisTaskRefusedStaysPending(t *testing.T) {
	now := at("10:01")
	f := &analysisFixture{refuse: true}
	s := New("", func() time.Time { return now })
	s.EnsureAnalysis("2026-10-20", f.due, f.run)
	s.Tick()
	task, _ := s.Task(AnalysisID("2026-10-20"))
	if task.State != TaskPending {
		t.Fatalf("a task that could not start is %q", task.State)
	}
	f.refuse = false
	s.Tick()
	if len(f.started) != 1 {
		t.Fatalf("did not retry: %v", f.started)
	}
}

func TestAnalysisTaskCancelRestore(t *testing.T) {
	now := at("09:00")
	f := &analysisFixture{}
	s := New("", func() time.Time { return now })
	s.EnsureAnalysis("2026-10-20", f.due, f.run)
	if !s.Cancel(AnalysisID("2026-10-20")) {
		t.Fatal("cancel failed")
	}
	now = at("10:01")
	s.Tick()
	if len(f.started) != 0 {
		t.Fatalf("a cancelled day ran: %v", f.started)
	}
	if !s.Restart(AnalysisID("2026-10-20")) {
		t.Fatal("restore failed")
	}
	task, _ := s.Task(AnalysisID("2026-10-20"))
	if task.State != TaskPending {
		t.Fatalf("restored task is %q", task.State)
	}
	s.Tick()
	if len(f.started) != 1 {
		t.Fatalf("the restored day did not run: %v", f.started)
	}
}

func TestAnalysisTaskStartAllRunsWaitingDaysNow(t *testing.T) {
	now := at("09:00")
	f := &analysisFixture{}
	s := New("", func() time.Time { return now })
	s.EnsureAnalysis("2026-10-20", f.due, f.run)
	now = at("09:30")
	s.Tick()
	if len(f.started) != 0 {
		t.Fatalf("a future task ran: %v", f.started)
	}
	// Start all makes the waiting days due now.
	s.StartAll()
	s.Tick()
	if len(f.started) != 1 {
		t.Fatalf("start all did not run the waiting day: %v", f.started)
	}
}

func TestAnalysisTaskFailureAndRetry(t *testing.T) {
	now := at("10:01")
	f := &analysisFixture{}
	s := New("", func() time.Time { return now })
	s.EnsureAnalysis("2026-10-20", f.due, f.run)
	s.Tick()
	if !s.FinishTask(AnalysisID("2026-10-20"), "没有 Pi") {
		t.Fatal("finishing the running task failed")
	}
	task, _ := s.Task(AnalysisID("2026-10-20"))
	if task.State != TaskFailed || task.Error != "没有 Pi" {
		t.Fatalf("failed task is %+v", task)
	}
	s.Tick()
	if len(f.started) != 1 {
		t.Fatalf("a failed task retried without being asked: %v", f.started)
	}
	if !s.Restart(AnalysisID("2026-10-20")) {
		t.Fatal("retry failed")
	}
	s.Tick()
	if len(f.started) != 2 {
		t.Fatalf("the retried task did not run: %v", f.started)
	}
}

func TestAnalysisTasksAndDailyJobsPersist(t *testing.T) {
	now := at("10:01")
	f := &analysisFixture{}
	s, path := newAnalysisScheduler(t, &now, f)
	s.Tick()
	if len(f.started) != 1 {
		t.Fatalf("started %v", f.started)
	}
	// A new process sees the running task as interrupted and waits again;
	// the finished one stays done.
	f2 := &analysisFixture{}
	s2 := New(path, func() time.Time { return now })
	s2.BindAnalysis("2026-10-20", f2.due, f2.run)
	s2.BindAnalysis("2026-10-19", f2.due, f2.run)
	task, ok := s2.Task(AnalysisID("2026-10-20"))
	if !ok || task.State != TaskPending {
		t.Fatalf("an interrupted task reloaded as %+v, %v", task, ok)
	}
	s2.Tick()
	if len(f2.started) != 1 || f2.started[0] != "2026-10-20" {
		t.Fatalf("the interrupted task did not run again: %v", f2.started)
	}
	// Cancellations survive a launch too.
	if !s2.Cancel(AnalysisID("2026-10-19")) {
		t.Fatal("cancel failed")
	}
	s3 := New(path, func() time.Time { return now })
	task, _ = s3.Task(AnalysisID("2026-10-19"))
	if task.State != TaskCancelled {
		t.Fatalf("a cancelled task reloaded as %q", task.State)
	}
}

func TestOldScheduleFileStillLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schedule.json")
	if err := os.WriteFile(path, []byte(`{"collector:photos":"2026-10-24T10:00:00+08:00"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(path, func() time.Time { return at("11:00") })
	last, ok := s.Last("collector:photos")
	if !ok || last.Hour() != 10 || last.Minute() != 0 {
		t.Fatalf("the old last-run record is %v, %v", last, ok)
	}
	if len(s.Tasks()) != 0 {
		t.Fatalf("the old file invented tasks: %+v", s.Tasks())
	}
}

// The queue runs in due order, not in the order the days were registered.
func TestAnalysisTaskRunsInDueOrder(t *testing.T) {
	now := at("09:00")
	f := &analysisFixture{}
	s := New("", func() time.Time { return now })
	s.EnsureAnalysis("2026-10-21", func(time.Time) time.Time { return at("11:00") }, f.run)
	s.EnsureAnalysis("2026-10-20", func(time.Time) time.Time { return at("10:00") }, f.run)
	now = at("11:01")
	s.Tick()
	if len(f.started) != 1 || f.started[0] != "2026-10-20" {
		t.Fatalf("started %v, want the 10:00 day first", f.started)
	}
	if !s.FinishTask(AnalysisID("2026-10-20"), "") {
		t.Fatal("finish failed")
	}
	s.Tick()
	if len(f.started) != 2 || f.started[1] != "2026-10-21" {
		t.Fatalf("second start is %v", f.started)
	}
}

// Restarting a cancelled day keeps its original time: not before it, and
// overdue ones run on the next tick.
func TestRestartKeepsTheOriginalTime(t *testing.T) {
	now := at("09:00")
	f := &analysisFixture{}
	s := New("", func() time.Time { return now })
	s.EnsureAnalysis("2026-10-20", func(time.Time) time.Time { return at("10:00") }, f.run)
	if !s.Cancel(AnalysisID("2026-10-20")) {
		t.Fatal("cancel failed")
	}
	now = at("09:30")
	if !s.Restart(AnalysisID("2026-10-20")) {
		t.Fatal("restart failed")
	}
	s.Tick()
	if len(f.started) != 0 {
		t.Fatalf("a restarted day ran before its time: %v", f.started)
	}
	now = at("10:01")
	s.Tick()
	if len(f.started) != 1 {
		t.Fatalf("an overdue restarted day did not run: %v", f.started)
	}
}

// Hold stops the automatic start; a task the user starts by hand runs.
func TestHoldOnlyStopsAutomaticStarts(t *testing.T) {
	now := at("10:01")
	f := &analysisFixture{}
	s := New("", func() time.Time { return now })
	s.EnsureAnalysis("2026-10-20", f.due, f.run)
	s.Hold(true)
	s.Tick()
	if len(f.started) != 0 {
		t.Fatalf("a held task started: %v", f.started)
	}
	if !s.Start(AnalysisID("2026-10-20")) {
		t.Fatal("start failed")
	}
	s.Tick()
	if len(f.started) != 1 {
		t.Fatalf("a hand-started task did not run: %v", f.started)
	}
}

// A task the user starts by hand runs before background ones, even when its
// due time is later.
func TestUserPriorityRunsFirst(t *testing.T) {
	now := at("10:01")
	f := &analysisFixture{}
	s := New("", func() time.Time { return now })
	s.EnsureAnalysis("2026-10-20", f.due, f.run)
	s.EnsureAnalysis("2026-10-21", f.due, f.run)
	if !s.Start(AnalysisID("2026-10-21")) {
		t.Fatal("start failed")
	}
	s.Tick()
	if len(f.started) != 1 || f.started[0] != "2026-10-21" {
		t.Fatalf("started %v, want the hand-started day first", f.started)
	}
}
