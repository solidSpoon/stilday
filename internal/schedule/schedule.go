// Package schedule runs daily jobs once a day, after their time, and keeps
// the record of when each ran. That record lives in the scheduler's own
// file: a job never has to create a folder in the data it manages just to
// tell the scheduler it already ran.
//
// Besides the fixed daily jobs, the scheduler owns the per-day analysis
// tasks. A task is registered when a day has material the model has not
// read yet; it waits until the daily analysis time, then the app starts
// that day's conversation. Cancel, restore and retry are task state
// changes, persisted next to the daily jobs.
package schedule

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// TaskState is where one day's analysis task stands.
type TaskState string

const (
	TaskPending   TaskState = "pending"
	TaskRunning   TaskState = "running"
	TaskDone      TaskState = "done"
	TaskCancelled TaskState = "cancelled"
	TaskFailed    TaskState = "failed"
)

// analysisPrefix names a per-day task in the state file and in the rail.
const analysisPrefix = "analysis:"

// AnalysisID is the scheduler's ID for one day's analysis.
func AnalysisID(day string) string { return analysisPrefix + day }

// IsAnalysis reports whether id names a per-day analysis task.
func IsAnalysis(id string) bool { return strings.HasPrefix(id, analysisPrefix) }

// AnalysisDay returns the day key inside an analysis ID.
func AnalysisDay(id string) string { return strings.TrimPrefix(id, analysisPrefix) }

// Task is one day's analysis job. Due is when the scheduler may start it;
// the task states mirror what the collector page's rail shows.
// Kind is what a task is: a one-off execution, or a schedule that keeps
// running again.
type Kind uint8

const (
	// Once is one execution: an analysis of a day.
	Once Kind = iota
	// Recurring is a task the scheduler runs again after each run.
	Recurring
)

type Task struct {
	ID    string    `json:"id"`
	Day   string    `json:"day,omitempty"`
	Kind  Kind      `json:"kind,omitempty"`
	State TaskState `json:"state,omitempty"`
	// Priority orders the runnable tasks: a task the user started by hand
	// goes before the background ones, whatever their due times.
	Priority   int       `json:"priority,omitempty"`
	Due        time.Time `json:"due,omitempty"`
	Registered time.Time `json:"registered,omitempty"`
	Started    time.Time `json:"started,omitempty"`
	Finished   time.Time `json:"finished,omitempty"`
	Error      string    `json:"error,omitempty"`

	// Spec is the execution plan of a recurring task.
	Spec Spec `json:"spec,omitempty"`
	// Enabled turns a recurring task's plan on or off.
	Enabled bool `json:"enabled,omitempty"`
	// SkipUntil is the occurrence a cancelled chain head skipped: the
	// scheduler runs the next one after it.
	SkipUntil time.Time `json:"skip_until,omitempty"`
	// Interrupted marks a task that was running when the app quit; a
	// restart resumes it only inside the catch-up window.
	Interrupted bool `json:"-"`

	// The app side of a recurring task; rebound on every launch.
	run func() bool
}

// analysisBinding is the app side of a task: when it may start, and the
// conversation that starts it. Bindings are not persisted; the app binds
// them again for the tasks loaded at launch.
type analysisBinding struct {
	due func(now time.Time) time.Time
	run func(day string) bool
}

// Scheduler starts the registered jobs once a day, after their time, keeps
// when each last ran, and moves the per-day analysis tasks through their
// states.
type Scheduler struct {
	path string
	now  func() time.Time
	last map[string]time.Time

	tasks   []Task
	binding map[string]*analysisBinding
	// hold keeps tasks from starting on their own, for a day whose
	// automatic analysis is switched off; a task the user starts by hand
	// runs anyway.
	hold    bool
	manual  map[string]bool
	catchUp time.Duration
}

// stateFile is the scheduler's file. The old file was a bare map of
// job ID to last-run time; load still reads that shape.
type stateFile struct {
	Last  map[string]string `json:"last,omitempty"`
	Tasks []Task            `json:"tasks,omitempty"`
}

// New returns a scheduler whose state lives at path. An empty path keeps it
// in memory only.
func New(path string, now func() time.Time) *Scheduler {
	if now == nil {
		now = time.Now
	}
	s := &Scheduler{
		path:    path,
		now:     now,
		last:    map[string]time.Time{},
		binding: map[string]*analysisBinding{},
		manual:  map[string]bool{},
		catchUp: time.Hour,
	}
	s.load()
	return s
}

// AddRecurring ensures a recurring task: one row in the queue, run again
// after each run. Calling it again after a restart binds the loaded row to
// the app's run function rather than adding a second one.
func (s *Scheduler) AddRecurring(id string, spec Spec, enabled bool, run func() bool) {
	for i := range s.tasks {
		if s.tasks[i].ID != id {
			continue
		}
		t := &s.tasks[i]
		t.Kind = Recurring
		t.Spec, t.Enabled, t.run = spec, enabled, run
		s.save()
		return
	}
	s.tasks = append(s.tasks, Task{ID: id, Kind: Recurring, Spec: spec, Enabled: enabled, run: run})
	s.save()
}

// Mark records that id ran now, so a later tick does not start it again
// today.
func (s *Scheduler) Mark(id string) {
	s.last[id] = s.now()
	s.save()
}

// Last reports when id last ran.
func (s *Scheduler) Last(id string) (time.Time, bool) {
	t, ok := s.last[id]
	return t, ok
}

// Tick starts every daily job whose time has come and that has not run
// since then, then the analysis tasks that are due. A job or task whose Run
// reports false is left for the next tick.
func (s *Scheduler) Tick() {
	now := s.now()
	changed := false
	for i := range s.tasks {
		t := &s.tasks[i]
		if t.Kind != Recurring || !t.Enabled || t.run == nil {
			continue
		}
		at, ok := t.Spec.Today(now)
		if !ok || now.Before(at) {
			continue
		}
		if s.catchUp > 0 && now.Sub(at) > s.catchUp {
			continue
		}
		if !t.SkipUntil.IsZero() && !at.After(t.SkipUntil) {
			continue
		}
		if last, ok := s.last[t.ID]; ok && !last.Before(at) {
			continue
		}
		if !t.run() {
			continue
		}
		s.last[t.ID] = now
		changed = true
	}
	if s.runDue(now) {
		changed = true
	}
	if changed {
		s.save()
	}
}

// RunDue starts the due analysis tasks without touching the daily jobs.
// The UI calls it after a turn settles, so a queued day does not wait for
// the next frame.
func (s *Scheduler) RunDue() {
	if s.runDue(s.now()) {
		s.save()
	}
}

// runDue starts at most one pending task whose time has come. It stops as
// soon as the app reports that a task started; a task the app could not
// start is left pending for the next tick.
func (s *Scheduler) runDue(now time.Time) bool {
	for _, t := range s.tasks {
		if t.State == TaskRunning {
			return false
		}
	}
	var due []int
	for i := range s.tasks {
		t := &s.tasks[i]
		if t.Kind != Once || t.State != TaskPending || t.Due.IsZero() || now.Before(t.Due) {
			continue
		}
		if t.Interrupted && s.catchUp > 0 && now.Sub(t.Due) > s.catchUp {
			// Interrupted by a quit, and too late to resume: it failed.
			t.State = TaskFailed
			t.Finished = now
			t.Error = "中断超时"
			t.Interrupted = false
			s.save()
			continue
		}
		t.Interrupted = false
		if s.hold && !s.manual[t.ID] {
			continue
		}
		if b := s.binding[t.ID]; b == nil || b.run == nil {
			continue
		}
		due = append(due, i)
	}
	// A task the user started by hand goes first; otherwise the queue runs
	// in due order, so an 11:00 task never gets in front of a 10:00 one.
	sort.SliceStable(due, func(a, b int) bool {
		ta, tb := &s.tasks[due[a]], &s.tasks[due[b]]
		if ta.Priority != tb.Priority {
			return ta.Priority > tb.Priority
		}
		return ta.Due.Before(tb.Due)
	})
	for _, i := range due {
		t := &s.tasks[i]
		b := s.binding[t.ID]
		// Mark it running before the app can settle the turn: a fast
		// agent could otherwise finish before the scheduler records the
		// start, leaving a running task with no conversation.
		t.State = TaskRunning
		t.Started = now
		t.Error = ""
		if b.run(t.Day) {
			return true
		}
		// The app could not take the slot: the day waits for the next
		// tick. An app that settled the task itself (failed, or deferred
		// to the next daily time) keeps that state.
		if t.State == TaskRunning {
			t.State = TaskPending
			t.Started = time.Time{}
		}
	}
	return false
}

// EnsureAnalysis registers a task for a day that still needs an analysis,
// or binds an existing one. A done task is reopened: the day's material
// changed again since it was analyzed. A cancelled or failed task keeps its
// state until the user restores or retries it.
func (s *Scheduler) EnsureAnalysis(day string, due func(now time.Time) time.Time, run func(day string) bool) {
	if day == "" {
		return
	}
	id := AnalysisID(day)
	s.binding[id] = &analysisBinding{due: due, run: run}
	now := s.now()
	for i := range s.tasks {
		t := &s.tasks[i]
		if t.ID != id {
			continue
		}
		if t.State == TaskDone {
			t.State = TaskPending
			t.Due = due(now)
			t.Started = time.Time{}
			t.Finished = time.Time{}
			t.Error = ""
			s.save()
		}
		return
	}
	s.tasks = append(s.tasks, Task{
		ID: id, Day: day, State: TaskPending, Due: due(now), Registered: now,
	})
	s.save()
}

// BindAnalysis attaches the app's due and run to a task loaded from disk,
// without adding or reopening it.
func (s *Scheduler) BindAnalysis(day string, due func(now time.Time) time.Time, run func(day string) bool) {
	if day == "" {
		return
	}
	s.binding[AnalysisID(day)] = &analysisBinding{due: due, run: run}
}

// Queue returns every queue row in the order it was added, so the list
// does not jump around as states change.
func (s *Scheduler) Queue() []Task {
	out := make([]Task, len(s.tasks))
	copy(out, s.tasks)
	return out
}

// Ahead is how many runnable tasks go before id: the running one, then the
// ready ones by priority and due time.
func (s *Scheduler) Ahead(id string) int {
	now := s.now()
	n := 0
	var ready []Task
	for _, t := range s.tasks {
		if t.Kind != Once {
			continue
		}
		switch {
		case t.State == TaskRunning:
			n++
		case t.State == TaskPending && !t.Due.IsZero() && !t.Due.After(now):
			ready = append(ready, t)
		}
	}
	sort.SliceStable(ready, func(i, j int) bool {
		if ready[i].Priority != ready[j].Priority {
			return ready[i].Priority > ready[j].Priority
		}
		return ready[i].Due.Before(ready[j].Due)
	})
	for _, t := range ready {
		if t.ID == id {
			return n
		}
		n++
	}
	return n
}

// Tasks returns the one-off tasks, in registration order. Recurring tasks
// are queue rows too, but the app draws them from its own settings.
func (s *Scheduler) Tasks() []Task {
	out := make([]Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		if t.Kind == Once {
			out = append(out, t)
		}
	}
	return out
}

// Task returns one task by ID.
func (s *Scheduler) Task(id string) (Task, bool) {
	for _, t := range s.tasks {
		if t.ID == id {
			return t, true
		}
	}
	return Task{}, false
}

// RunningTask returns the analysis task holding the conversation slot.
func (s *Scheduler) RunningTask() (Task, bool) {
	for _, t := range s.tasks {
		if t.State == TaskRunning {
			return t, true
		}
	}
	return Task{}, false
}

// CountPending counts the tasks waiting to run.
func (s *Scheduler) CountPending() int {
	n := 0
	for _, t := range s.tasks {
		if t.State == TaskPending {
			n++
		}
	}
	return n
}

// CanStart reports whether there is a task the user could run.
func (s *Scheduler) CanStart() bool {
	return s.CountPending() > 0
}

// HasRunning reports whether one task holds the conversation slot.
func (s *Scheduler) HasRunning() bool {
	_, ok := s.RunningTask()
	return ok
}

// CanCancel reports whether there is anything left to cancel.
func (s *Scheduler) CanCancel() bool {
	for _, t := range s.tasks {
		if t.State == TaskRunning || t.State == TaskPending {
			return true
		}
	}
	return false
}

// SetCatchUp is how late an interrupted task may resume on launch.
func (s *Scheduler) SetCatchUp(d time.Duration) { s.catchUp = d }

// SkipUntil makes a chain head skip the occurrence at or before at.
func (s *Scheduler) SkipUntil(id string, at time.Time) {
	for i := range s.tasks {
		if s.tasks[i].ID == id {
			s.tasks[i].SkipUntil = at
			s.save()
			return
		}
	}
}

// Hold keeps the waiting tasks from starting by themselves, as the daily
// analysis's switch does. Tasks the user starts by hand still run.
func (s *Scheduler) Hold(on bool) { s.hold = on }

// Held reports whether waiting tasks are held back.
func (s *Scheduler) Held() bool { return s.hold }

// StartAll makes every waiting task due now, as the user's "启动所有"
// does; the tasks go ahead of the background ones.
func (s *Scheduler) StartAll() {
	s.releaseAll(true)
}

// ReleaseAll makes every waiting task due now, as the daily analysis job
// does; they keep the background priority.
func (s *Scheduler) ReleaseAll() {
	s.releaseAll(false)
}

func (s *Scheduler) releaseAll(userPriority bool) {
	now := s.now()
	changed := false
	for i := range s.tasks {
		t := &s.tasks[i]
		if t.State != TaskPending {
			continue
		}
		t.Due = now
		if userPriority {
			t.Priority = 1
			s.manual[t.ID] = true
		}
		changed = true
	}
	if changed {
		s.save()
	}
}

// ReschedulePending moves the waiting tasks to a new daily time, as a change
// to AutoTime requires. The running task is not affected.
func (s *Scheduler) ReschedulePending(due time.Time) {
	changed := false
	for i := range s.tasks {
		t := &s.tasks[i]
		if t.State != TaskPending || t.Due.Equal(due) {
			continue
		}
		t.Due = due
		changed = true
	}
	if changed {
		s.save()
	}
}

// Cancel marks one task cancelled. The caller stops the day's conversation
// when it is the running task.
func (s *Scheduler) Cancel(id string) bool {
	for i := range s.tasks {
		t := &s.tasks[i]
		if t.ID != id {
			continue
		}
		if t.State != TaskPending && t.State != TaskRunning {
			return false
		}
		t.State = TaskCancelled
		t.Finished = s.now()
		delete(s.manual, id)
		s.save()
		return true
	}
	return false
}

// CancelAll cancels the waiting and running tasks. The caller stops the
// running conversation.
func (s *Scheduler) CancelAll() {
	changed := false
	now := s.now()
	s.manual = map[string]bool{}
	for i := range s.tasks {
		t := &s.tasks[i]
		if t.State == TaskPending || t.State == TaskRunning {
			t.State = TaskCancelled
			t.Finished = now
			changed = true
		}
	}
	if changed {
		s.save()
	}
}

// Start makes one task due now: a waiting task keeps waiting and a failed
// one re-enters the queue, both at the current time.
func (s *Scheduler) Start(id string) bool {
	for i := range s.tasks {
		t := &s.tasks[i]
		if t.ID != id {
			continue
		}
		switch t.State {
		case TaskPending:
		case TaskFailed:
			t.State = TaskPending
			t.Started = time.Time{}
			t.Finished = time.Time{}
			t.Error = ""
		default:
			return false
		}
		t.Due = s.now()
		t.Priority = 1
		s.manual[id] = true
		s.save()
		return true
	}
	return false
}

// Restart puts a cancelled or failed task back in the queue at the time it
// already had. A due time that has already passed makes it run on the next
// tick, as any overdue task does.
func (s *Scheduler) Restart(id string) bool {
	for i := range s.tasks {
		t := &s.tasks[i]
		if t.ID != id || (t.State != TaskCancelled && t.State != TaskFailed) {
			continue
		}
		t.State = TaskPending
		t.Started = time.Time{}
		t.Finished = time.Time{}
		t.Error = ""
		delete(s.manual, id)
		s.save()
		return true
	}
	return false
}

// Interrupt returns the running task to the waiting list, due at the time
// the caller gives it. A collector run is about to replace the day's
// material, so the interrupted day is not marked done or cancelled.
func (s *Scheduler) Interrupt(id string, due time.Time) bool {
	for i := range s.tasks {
		t := &s.tasks[i]
		if t.ID != id || t.State != TaskRunning {
			continue
		}
		t.State = TaskPending
		t.Due = due
		t.Started = time.Time{}
		t.Error = ""
		delete(s.manual, id)
		s.save()
		return true
	}
	return false
}

// FinishTask settles the running task: an empty err marks it done, anything
// else marks it failed and keeps the message for the rail.
func (s *Scheduler) FinishTask(id, errText string) bool {
	for i := range s.tasks {
		t := &s.tasks[i]
		if t.ID != id || t.State != TaskRunning {
			continue
		}
		t.Finished = s.now()
		delete(s.manual, id)
		if errText == "" {
			t.State = TaskDone
			t.Error = ""
		} else {
			t.State = TaskFailed
			t.Error = errText
		}
		s.save()
		return true
	}
	return false
}

// Remove drops a task whose day no longer needs an analysis.
func (s *Scheduler) Remove(id string) bool {
	for i := range s.tasks {
		if s.tasks[i].ID != id {
			continue
		}
		s.tasks = append(s.tasks[:i], s.tasks[i+1:]...)
		delete(s.binding, id)
		s.save()
		return true
	}
	return false
}

// ClearCancelled drops the cancelled tasks the rail lists. Done tasks
// leave the list on their own; failed ones stay until they are restarted.
func (s *Scheduler) ClearCancelled() {
	kept := s.tasks[:0]
	changed := false
	for _, t := range s.tasks {
		if t.State == TaskCancelled {
			changed = true
			continue
		}
		kept = append(kept, t)
	}
	s.tasks = kept
	if changed {
		s.save()
	}
}

// HasCancelled reports whether there is a cancelled task to clear.
func (s *Scheduler) HasCancelled() bool {
	for _, t := range s.tasks {
		if t.State == TaskCancelled {
			return true
		}
	}
	return false
}

func (s *Scheduler) load() {
	if s.path == "" {
		return
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(b, &raw) != nil {
		return
	}
	if v, ok := raw["last"]; ok {
		var last map[string]string
		if json.Unmarshal(v, &last) == nil {
			for id, stamp := range last {
				if t, err := time.Parse(time.RFC3339, stamp); err == nil {
					s.last[id] = t
				}
			}
		}
	} else {
		// The old file was the map itself.
		for id, v := range raw {
			var stamp string
			if json.Unmarshal(v, &stamp) == nil {
				if t, err := time.Parse(time.RFC3339, stamp); err == nil {
					s.last[id] = t
				}
			}
		}
	}
	if v, ok := raw["tasks"]; ok {
		_ = json.Unmarshal(v, &s.tasks)
	}
	// A task that was running when the app stopped was interrupted: it
	// waits again, with its material still unread.
	for i := range s.tasks {
		if s.tasks[i].State == TaskRunning {
			s.tasks[i].State = TaskPending
			s.tasks[i].Started = time.Time{}
			s.tasks[i].Interrupted = true
		}
	}
}

func (s *Scheduler) save() {
	if s.path == "" {
		return
	}
	f := stateFile{Tasks: s.tasks}
	if len(s.last) > 0 {
		f.Last = make(map[string]string, len(s.last))
		for id, t := range s.last {
			f.Last[id] = t.Format(time.RFC3339)
		}
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(s.path), 0o755)
	_ = os.WriteFile(s.path, b, 0o644)
}

// Spec is when a scheduled job runs: ISO weekdays (1 is Monday, 7 Sunday;
// no days means every day) and a local clock, "HH:MM".
type Spec struct {
	Days []int  `json:"days,omitempty"`
	Time string `json:"time"`
	// Times are further runs on the same day, after Time.
	Times []string `json:"times,omitempty"`
}

// AllTimes is every clock the spec runs at, Time first.
func (s Spec) AllTimes() []string {
	out := make([]string, 0, 1+len(s.Times))
	if s.Time != "" {
		out = append(out, s.Time)
	}
	out = append(out, s.Times...)
	return out
}

// HasDay reports whether the spec runs on an ISO weekday.
func (s Spec) HasDay(iso int) bool {
	if len(s.Days) == 0 {
		return true
	}
	for _, d := range s.Days {
		if d == iso {
			return true
		}
	}
	return false
}

// clockOn is the spec's time on one day.
func (sp Spec) clockOn(day time.Time, clock string) (time.Time, error) {
	parts := strings.Split(strings.TrimSpace(clock), ":")
	if len(parts) != 2 {
		return time.Time{}, fmt.Errorf("时间要写成 HH:MM")
	}
	h, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return time.Time{}, err
	}
	m, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return time.Time{}, fmt.Errorf("时间要写成 HH:MM")
	}
	return time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, day.Location()), nil
}

// isoWeekday is time.Weekday in ISO numbering: Monday 1 through Sunday 7.
func isoWeekday(t time.Time) int {
	w := int(t.Weekday())
	if w == 0 {
		return 7
	}
	return w
}

// Today is today's occurrence: the latest one already passed, or the
// earliest still ahead. ok is false when the spec skips today.
func (sp Spec) Today(now time.Time) (time.Time, bool) {
	if !sp.HasDay(isoWeekday(now)) {
		return time.Time{}, false
	}
	var latest, first time.Time
	for _, c := range sp.AllTimes() {
		at, err := sp.clockOn(now, c)
		if err != nil {
			continue
		}
		if !at.After(now) {
			if at.After(latest) {
				latest = at
			}
		} else if first.IsZero() || at.Before(first) {
			first = at
		}
	}
	if !latest.IsZero() {
		return latest, true
	}
	if !first.IsZero() {
		return first, true
	}
	return time.Time{}, false
}

// Next is the first occurrence strictly after now.
func (sp Spec) Next(now time.Time) time.Time {
	best := time.Time{}
	for i := 0; i < 9; i++ {
		day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, i)
		if !sp.HasDay(isoWeekday(day)) {
			continue
		}
		for _, c := range sp.AllTimes() {
			at, err := sp.clockOn(day, c)
			if err != nil || !at.After(now) {
				continue
			}
			if best.IsZero() || at.Before(best) {
				best = at
			}
		}
		if !best.IsZero() && i >= 1 {
			break
		}
	}
	if best.IsZero() {
		return now.Add(24 * time.Hour)
	}
	return best
}

// EveryDay reports whether the spec runs every day.
func (s Spec) EveryDay() bool { return len(s.Days) == 0 }

// Weekdays reports whether the spec runs Monday through Friday.
func (s Spec) Weekdays() bool {
	if len(s.Days) != 5 {
		return false
	}
	for _, d := range s.Days {
		if d < 1 || d > 5 {
			return false
		}
	}
	return true
}
