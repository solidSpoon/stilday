// Package analysis queues the days that still need an AI pass. It owns the
// queue state machine and calls back into the app for the day switch, the
// fingerprint it records, and the agent it stops.
package analysis

import (
	"sort"
	"time"

	"stilday/internal/artifact"
)

// maxDone is how many finished days the task rail remembers.
const maxDone = 20

// Done is a day this session analyzed, for the task list.
type Done struct {
	Day string
	At  time.Time
}

// Hooks are the app services the queue needs. The queue itself holds no
// window, store or disk access, so its transitions can be tested on their
// own.
type Hooks struct {
	// Pending returns the days whose collectors changed, newest first.
	Pending func() []string
	// StartDay switches the app to a day and starts its analysis. It
	// reports whether an analysis actually began.
	StartDay func(key string) bool
	// MarkAnalyzed records that the model consumed the day's material.
	MarkAnalyzed func(key string)
	// Stop aborts the analysis running on the current day.
	Stop func()
}

// Queue runs the days that still need an AI pass, one at a time.
// A re-analysis of one day replaces that day's events, so the queue only
// ever moves forward.
type Queue struct {
	// Queue is what still waits; Current is the day being analyzed.
	Queue   []string
	Current string
	Running bool
	Paused  bool
	Auto    bool
	// Cancelled remembers this session's cancellations, by day.
	Cancelled map[string]time.Time
	// Done is the recent history the task rail shows.
	Done []Done

	hooks Hooks
}

// Pending returns the changed days the user has not cancelled, newest
// first.
func (q *Queue) Pending() []string {
	if q.hooks.Pending == nil {
		return nil
	}
	return q.FilterCancelled(q.hooks.Pending())
}

// FilterCancelled drops the days the user cancelled from a queue.
func (q *Queue) FilterCancelled(days []string) []string {
	out := make([]string, 0, len(days))
	for _, key := range days {
		if !q.IsCancelled(key) {
			out = append(out, key)
		}
	}
	return out
}

func (q *Queue) IsCancelled(key string) bool {
	_, ok := q.Cancelled[key]
	return ok
}

func (q *Queue) CancelledAt(key string) (time.Time, bool) {
	at, ok := q.Cancelled[key]
	return at, ok
}

// cancelledDays lists the days cancelled this session, newest first.
func (q *Queue) CancelledDays() []string {
	days := make([]string, 0, len(q.Cancelled))
	for key := range q.Cancelled {
		days = append(days, key)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(days)))
	return days
}

// cancel takes a day out of the queue and remembers it as cancelled, so
// the nightly run does not put it back.
func (q *Queue) Cancel(key string) {
	kept := q.Queue[:0]
	for _, queued := range q.Queue {
		if queued != key {
			kept = append(kept, queued)
		}
	}
	q.Queue = kept
	if q.Cancelled == nil {
		q.Cancelled = map[string]time.Time{}
	}
	q.Cancelled[key] = time.Now()
}

// restore forgets a cancellation: the day is pending again.
func (q *Queue) Restore(key string) {
	delete(q.Cancelled, key)
}

// startPending queues every pending day and drains the queue.
func (q *Queue) StartPending() {
	if q.Running {
		return
	}
	q.Paused = false
	pending := q.Pending()
	if len(pending) == 0 {
		return
	}
	q.Queue = pending
	q.run(false)
}

// SetQueue replaces the waiting days without starting them. The nightly
// collector uses it to show the fresh pending days.
func (q *Queue) SetQueue(days []string) {
	q.Queue = days
}

// Start queues explicit days and drains them, unless a run is in progress.
func (q *Queue) Start(days []string, auto bool) {
	if len(days) == 0 {
		return
	}
	q.Queue = days
	q.run(auto)
}

// run drains the days already queued, one at a time.
func (q *Queue) run(auto bool) {
	if q.Running || len(q.Queue) == 0 {
		return
	}
	q.Auto = auto
	q.Running = true
	q.next()
}

// next starts the first usable day in the queue. A malformed or cancelled
// key is skipped; an empty queue ends the run.
func (q *Queue) next() {
	if q.Paused {
		q.halt()
		return
	}
	for len(q.Queue) > 0 {
		key := q.Queue[0]
		if _, err := artifact.ParseDayKey(key); err != nil || q.IsCancelled(key) {
			q.Queue = q.Queue[1:]
			continue
		}
		break
	}
	if len(q.Queue) == 0 {
		q.halt()
		return
	}
	key := q.Queue[0]
	q.Queue = q.Queue[1:]
	q.Current = key
	if q.hooks.StartDay != nil && !q.hooks.StartDay(key) {
		// No Pi or no key: stop rather than spin through the queue.
		q.halt()
	}
}

// halt ends the run without touching the remaining queue. Resume or
// restart picks it up again.
func (q *Queue) halt() {
	q.Running = false
	q.Auto = false
	q.Current = ""
}

// pause lets the current day finish but opens no new one.
func (q *Queue) Pause() {
	if !q.Running && len(q.Queue) == 0 {
		return
	}
	q.Paused = true
}

// resume continues a paused queue, or starts the pending days when no
// queue is left.
func (q *Queue) Resume() {
	q.Paused = false
	if q.Running {
		return
	}
	if len(q.Queue) > 0 {
		q.run(q.Auto)
		return
	}
	q.StartPending()
}

// restart clears cancellations and queues every pending day. A running day
// is allowed to finish; the reset queue starts right after it.
func (q *Queue) Restart() {
	q.Paused = false
	q.Cancelled = map[string]time.Time{}
	pending := q.Pending()
	if q.Running {
		out := make([]string, 0, len(pending))
		for _, key := range pending {
			if key != q.Current {
				out = append(out, key)
			}
		}
		q.Queue = out
		return
	}
	if len(pending) == 0 {
		return
	}
	q.Queue = pending
	q.run(false)
}

// cancelAll cancels every pending and queued day and stops the running one.
// The cancelled days stay listed, with their per-day Restore action.
func (q *Queue) CancelAll() {
	days := q.Pending()
	days = append(days, q.Queue...)
	if q.Current != "" {
		days = append(days, q.Current)
		q.halt()
	}
	for _, key := range days {
		q.Cancel(key)
	}
	q.Queue = nil
	q.Paused = true
	if q.hooks.Stop != nil {
		q.hooks.Stop()
	}
}

// finish records the day the model just read and moves to the next one. A
// failed turn keeps the old analyzed hash, so the next run retries it.
func (q *Queue) Finish(ok bool) {
	if !q.Running || q.Current == "" {
		return
	}
	key := q.Current
	q.Current = ""
	if ok {
		if q.hooks.MarkAnalyzed != nil {
			q.hooks.MarkAnalyzed(key)
		}
		delete(q.Cancelled, key)
		q.Done = append(q.Done, Done{Day: key, At: time.Now()})
		if len(q.Done) > maxDone {
			q.Done = q.Done[len(q.Done)-maxDone:]
		}
	}
	q.next()
}

// New returns a queue wired to the app services it calls. The zero queue
// has no hooks and is only useful in tests.
func New(hooks Hooks) Queue {
	return Queue{hooks: hooks, Cancelled: map[string]time.Time{}}
}
