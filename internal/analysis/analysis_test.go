package analysis

import "testing"

// queueFixture records what the app hooks would do, so the queue can be
// tested without a window, a store or Pi.
type queueFixture struct {
	pending  []string
	started  []string
	analyzed []string
	stopped  int
	fail     map[string]bool
}

func (f *queueFixture) queue() *Queue {
	q := New(Hooks{
		Pending:      func() []string { return f.pending },
		StartDay:     func(key string) bool { f.started = append(f.started, key); return !f.fail[key] },
		MarkAnalyzed: func(key string) { f.analyzed = append(f.analyzed, key) },
		Stop:         func() { f.stopped++ },
	})
	return &q
}

func TestAnalysisQueueDrainsPendingDays(t *testing.T) {
	f := &queueFixture{pending: []string{"2026-10-03", "2026-10-02", "2026-10-01"}}
	q := f.queue()
	q.StartPending()
	if !q.Running || q.Current != "2026-10-03" || len(q.Queue) != 2 {
		t.Fatalf("after start: running=%v current=%q queue=%v", q.Running, q.Current, q.Queue)
	}
	q.Finish(true)
	if len(f.analyzed) != 1 || f.analyzed[0] != "2026-10-03" {
		t.Fatalf("analyzed = %v", f.analyzed)
	}
	if q.Current != "2026-10-02" || len(q.Done) != 1 {
		t.Fatalf("after finish: current=%q done=%v", q.Current, q.Done)
	}
	q.Finish(false)
	if len(f.analyzed) != 1 {
		t.Fatalf("a failed turn was recorded as analyzed: %v", f.analyzed)
	}
	q.Finish(true)
	if q.Running || q.Current != "" || len(q.Queue) != 0 || len(q.Done) != 2 {
		t.Fatalf("after drain: running=%v current=%q queue=%v done=%v", q.Running, q.Current, q.Queue, q.Done)
	}
	if got := len(f.started); got != 3 {
		t.Fatalf("started %d days", got)
	}
}

func TestAnalysisQueuePauseResume(t *testing.T) {
	f := &queueFixture{pending: []string{"2026-10-03", "2026-10-02"}}
	q := f.queue()
	q.StartPending()
	q.Pause()
	q.Finish(true)
	if q.Running || q.Current != "" {
		t.Fatalf("a paused queue kept running: %v %q", q.Running, q.Current)
	}
	if len(q.Queue) != 1 || q.Queue[0] != "2026-10-02" {
		t.Fatalf("the queue lost a day: %v", q.Queue)
	}
	q.Resume()
	if !q.Running || q.Current != "2026-10-02" {
		t.Fatalf("resume did not continue: running=%v current=%q", q.Running, q.Current)
	}
}

func TestAnalysisQueueCancelRestoreRestart(t *testing.T) {
	f := &queueFixture{pending: []string{"2026-10-03", "2026-10-02", "2026-10-01"}}
	q := f.queue()
	q.Cancel("2026-10-02")
	if len(q.Pending()) != 2 {
		t.Fatalf("pending after cancel = %v", q.Pending())
	}
	q.StartPending()
	if q.Current != "2026-10-03" {
		t.Fatalf("cancelled day was started: %q", q.Current)
	}
	q.Queue = append(q.Queue, "2026-10-01") // duplicate queued day
	q.Cancel("2026-10-01")
	for _, key := range q.Queue {
		if key == "2026-10-01" {
			t.Fatalf("cancel left the day queued: %v", q.Queue)
		}
	}
	q.Restore("2026-10-02")
	q.Restart()
	if len(q.Cancelled) != 0 {
		t.Fatalf("restart kept cancellations: %v", q.Cancelled)
	}
	// The running day stays out of the queue it will restart after.
	for _, key := range q.Queue {
		if key == "2026-10-03" {
			t.Fatalf("restart queued the running day")
		}
	}
}

func TestAnalysisQueueCancelAllStops(t *testing.T) {
	f := &queueFixture{pending: []string{"2026-10-03", "2026-10-02"}}
	q := f.queue()
	q.StartPending()
	q.CancelAll()
	if !q.Paused || q.Running || q.Current != "" || len(q.Queue) != 0 {
		t.Fatalf("cancel all: paused=%v running=%v current=%q queue=%v", q.Paused, q.Running, q.Current, q.Queue)
	}
	if f.stopped != 1 {
		t.Fatalf("stop called %d times", f.stopped)
	}
	if len(q.Cancelled) != 2 {
		t.Fatalf("cancelled = %v", q.Cancelled)
	}
}

func TestAnalysisQueueStopsWithoutPi(t *testing.T) {
	f := &queueFixture{pending: []string{"2026-10-03", "2026-10-02"}, fail: map[string]bool{"2026-10-03": true}}
	q := f.queue()
	q.StartPending()
	if q.Running || q.Current != "" {
		t.Fatalf("a day that could not start kept the queue running")
	}
	if len(q.Queue) != 1 || q.Queue[0] != "2026-10-02" {
		t.Fatalf("remaining queue = %v", q.Queue)
	}
}
