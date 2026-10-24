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
		Pending:  func() []string { return f.pending },
		StartDay: func(key string) bool { f.started = append(f.started, key); return !f.fail[key] },
		MarkAnalyzed: func(key string) {
			f.analyzed = append(f.analyzed, key)
			kept := f.pending[:0]
			for _, day := range f.pending {
				if day != key {
					kept = append(kept, day)
				}
			}
			f.pending = kept
		},
		Stop: func() { f.stopped++ },
	})
	return &q
}

func TestAnalysisQueueStartPendingAppendsToRunning(t *testing.T) {
	f := &queueFixture{pending: []string{"2026-10-03"}}
	q := f.queue()
	q.StartPending()
	if !q.Running || q.Current != "2026-10-03" || len(q.Queue) != 0 {
		t.Fatalf("after start: running=%v current=%q queue=%v", q.Running, q.Current, q.Queue)
	}
	f.pending = []string{"2026-10-03", "2026-10-02"}
	q.StartPending()
	if len(q.Queue) != 1 || q.Queue[0] != "2026-10-02" {
		t.Fatalf("a new pending day was not appended: %v", q.Queue)
	}
	q.StartPending()
	if len(q.Queue) != 1 {
		t.Fatalf("a queued day was appended twice: %v", q.Queue)
	}
}

// A collector run stops the day and drops the queue, but keeps the pending
// days pending so the rail recomputes them afterwards.
func TestAnalysisQueueStopRunKeepsPending(t *testing.T) {
	f := &queueFixture{pending: []string{"2026-10-03", "2026-10-02"}}
	q := f.queue()
	q.StartPending()
	if !q.Running || q.Current != "2026-10-03" || len(q.Queue) != 1 {
		t.Fatalf("before stop: running=%v current=%q queue=%v", q.Running, q.Current, q.Queue)
	}
	q.StopRun()
	if q.Running || q.Current != "" || len(q.Queue) != 0 {
		t.Fatalf("after stop: running=%v current=%q queue=%v", q.Running, q.Current, q.Queue)
	}
	if f.stopped != 1 {
		t.Fatalf("stopped %d agents, want 1", f.stopped)
	}
	if len(q.Cancelled) != 0 {
		t.Fatalf("stop cancelled days: %v", q.Cancelled)
	}
	if got := q.Pending(); len(got) != 2 {
		t.Fatalf("pending after stop is %v", got)
	}
}

func TestAnalysisQueueActionStates(t *testing.T) {
	f := &queueFixture{pending: []string{"2026-10-03", "2026-10-02"}}
	q := f.queue()
	if !q.CanStart() || q.CanPause() || !q.CanCancel() {
		t.Fatalf("pending: start=%v pause=%v cancel=%v", q.CanStart(), q.CanPause(), q.CanCancel())
	}
	q.StartPending()
	// The run already covers every pending day, so starting all would do
	// nothing; pausing and cancelling still have work.
	if q.CanStart() || !q.CanPause() || !q.CanCancel() {
		t.Fatalf("running: start=%v pause=%v cancel=%v", q.CanStart(), q.CanPause(), q.CanCancel())
	}
	q.Pause()
	if !q.CanStart() || q.CanPause() {
		t.Fatalf("paused: start=%v pause=%v", q.CanStart(), q.CanPause())
	}
	q.CancelAll()
	if q.CanStart() || q.CanPause() || q.CanCancel() {
		t.Fatalf("cancelled: start=%v pause=%v cancel=%v", q.CanStart(), q.CanPause(), q.CanCancel())
	}
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

func TestAnalysisQueuePauseKeepsTheQueue(t *testing.T) {
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
	q.StartPending()
	if !q.Running || q.Current != "2026-10-02" {
		t.Fatalf("start all did not continue: running=%v current=%q", q.Running, q.Current)
	}
}

func TestAnalysisQueueCancelRestore(t *testing.T) {
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
	q.Cancel("2026-10-01")
	for _, key := range q.Queue {
		if key == "2026-10-01" {
			t.Fatalf("cancel left the day queued: %v", q.Queue)
		}
	}
	q.Restore("2026-10-02")
	if q.IsCancelled("2026-10-02") {
		t.Fatal("restore kept the cancellation")
	}
	if !q.CanStart() {
		t.Fatal("the restored day is not startable")
	}
	q.StartPending()
	if len(q.Queue) != 1 || q.Queue[0] != "2026-10-02" {
		t.Fatalf("the restored day was not queued: %v", q.Queue)
	}
}

func TestAnalysisQueueCancelAllStops(t *testing.T) {
	f := &queueFixture{pending: []string{"2026-10-03", "2026-10-02"}}
	q := f.queue()
	q.StartPending()
	q.CancelAll()
	if q.Paused || q.Running || q.Current != "" || len(q.Queue) != 0 {
		t.Fatalf("cancel all: paused=%v running=%v current=%q queue=%v", q.Paused, q.Running, q.Current, q.Queue)
	}
	if f.stopped != 1 {
		t.Fatalf("stop called %d times", f.stopped)
	}
	if len(q.Cancelled) != 2 {
		t.Fatalf("cancelled = %v", q.Cancelled)
	}
}

func TestAnalysisQueueCancelAllLeavesAChatAlone(t *testing.T) {
	f := &queueFixture{pending: []string{"2026-10-03"}}
	q := f.queue()
	q.CancelAll()
	if f.stopped != 0 {
		t.Fatal("cancel all stopped a conversation the queue was not running")
	}
	if len(q.Cancelled) != 1 {
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
