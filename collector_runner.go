package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A collector run is app-wide: the sidebar shows its progress, the day page
// waits for it, and the analysis queue must not start until it settles. The
// app owns that status here; the per-day results live in the day's
// collectors.json.

// beginCollection marks a run as started, on the UI thread.
func (a *app) beginCollection(id, progress string) {
	a.collecting = true
	a.collectID = id
	a.collectProgress = progress
	a.chat.preparing = true
	a.chat.progress = progress
}

// finishCollection clears the run status.
func (a *app) finishCollection() {
	a.collecting = false
	a.collectID = ""
	a.collectProgress = ""
	a.chat.preparing = false
	a.chat.progress = ""
}

// photosPlugin is the photos collector, or nil when this build has none.
func (a *app) photosPlugin() *photosCollector {
	p, _ := a.collectorByID(photosCollectorID).(*photosCollector)
	return p
}

// collectorEnv resolves the day's input for a run. The caller does this on
// the UI thread, so the background run never reads the app's config.
func (a *app) collectorEnv(p collector, day time.Time, dir string) (collectorEnv, error) {
	return collectorEnvFor(a.cfg, p, day, dir)
}

// collectorEnvFor is collectorEnv with the config passed in, for a worker
// that must not read the app.
func collectorEnvFor(cfg config, p collector, day time.Time, dir string) (collectorEnv, error) {
	env := collectorEnv{
		Day:      day,
		Dir:      dir,
		Folder:   p.Folder(),
		StateDir: pluginDir(cfg.storageRoot(), p.ID()),
		Photos: photoInput{
			Library:   photosLibrary(),
			MaxPhotos: cfg.MaxPhotos,
		},
	}
	start, end, err := cfg.photoWindow(day)
	if err != nil {
		return env, err
	}
	env.Photos.Start, env.Photos.End = start, end
	return env, nil
}

// collectorProgress publishes a plugin's progress to the sidebar. A prefix
// names the day during a range sync.
func (a *app) collectorProgress(id, prefix string) func(stage string, done, total int) {
	return func(stage string, done, total int) {
		if a.win == nil {
			return
		}
		text := stage
		if total > 0 {
			text = fmt.Sprintf("%s %d/%d", stage, done, total)
		}
		if prefix != "" {
			text = prefix + " · " + text
		}
		a.win.Update(func() {
			if !a.collecting || a.collectID != id {
				return
			}
			a.collectProgress = text
			a.chat.progress = text
		})
	}
}

// startCollector clears one collector's folder and runs it. auto is set
// when this run is part of the nightly pipeline, which the status records.
func (a *app) startCollector(p collector, auto bool) {
	if p == nil || a.chat.dir == "" || !a.takeConversationSlot() {
		return
	}
	day, dir := a.day, a.chat.dir
	env, err := a.collectorEnv(p, day, dir)
	if err != nil {
		a.chat.err = p.Name() + "：" + err.Error()
		return
	}
	if a.collectorDay.Items == nil {
		a.collectorDay.Items = map[string]collectorRun{}
	}
	run := a.collectorDay.Items[p.ID()]
	run.Status = "running"
	run.StartedAt = time.Now().Format(time.RFC3339)
	if auto {
		run.AutoAt = a.clockNow().Format(time.RFC3339)
	}
	run.FinishedAt = ""
	run.Error = ""
	a.collectorDay.Items[p.ID()] = run
	a.saveDayState(dir, a.collectorDay)
	syncDayManifest(dir, day, a.collectorDay, a.collectorList)
	a.beginCollection(p.ID(), "准备…")
	a.chat.progress = "准备采集…"

	if a.win == nil {
		// No window (tests): run inline so the result is immediately
		// observable and no worker mutates app state behind the caller.
		err := a.execCollector(p, dir, env)
		a.finishCollector(p, day, dir, env, err)
		return
	}
	go func() {
		err := a.execCollector(p, dir, env)
		a.win.Update(func() { a.finishCollector(p, day, dir, env, err) })
	}()
}

// execCollector replaces the plugin's folder and runs it, unless the
// plugin keeps what its folder already holds.
func (a *app) execCollector(p collector, dir string, env collectorEnv) error {
	folder := filepath.Join(dir, p.Folder())
	if !p.KeepsFolder() {
		_ = os.RemoveAll(folder)
	}
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return err
	}
	return p.Run(env, a.collectorProgress(p.ID(), ""))
}

func (a *app) finishCollector(p collector, day time.Time, dir string, env collectorEnv, err error) {
	a.finishCollection()
	if a.collectorDay.Items == nil {
		a.collectorDay.Items = map[string]collectorRun{}
	}
	run := a.collectorDay.Items[p.ID()]
	run.FinishedAt = time.Now().Format(time.RFC3339)
	if err != nil {
		run.Status = "error"
		run.Error = err.Error()
		a.chat.err = p.Name() + "：" + err.Error()
	} else {
		run.Status = "done"
		run.Error = ""
		a.chat.err = ""
		run.recordFingerprint(p.Fingerprint(env))
	}
	a.collectorDay.Items[p.ID()] = run
	a.saveDayState(dir, a.collectorDay)
	if err == nil {
		a.reloadMaterials(dir)
	}
	syncDayManifest(dir, day, a.collectorDay, a.collectorList)
	a.reloadCalendar()

	if len(a.collectQueue) > 0 {
		next := a.collectorByID(a.collectQueue[0])
		a.collectQueue = a.collectQueue[1:]
		a.startCollector(next, false)
	}
}

// startAllCollectors runs every registered collector in order.
func (a *app) startAllCollectors() {
	if a.chat.dir == "" || !a.takeConversationSlot() {
		return
	}
	a.collectQueue = nil
	for _, p := range a.collectorList {
		a.collectQueue = append(a.collectQueue, p.ID())
	}
	if len(a.collectQueue) == 0 {
		return
	}
	first := a.collectorByID(a.collectQueue[0])
	a.collectQueue = a.collectQueue[1:]
	a.startCollector(first, false)
}

// startCollectorID runs one collector by ID, as the page's Execute button
// does.
func (a *app) startCollectorID(id string) {
	p := a.collectorByID(id)
	if p == nil {
		return
	}
	a.startCollector(p, false)
}

// maybeAutoCollect starts each enabled plugin at its own daily time. A
// plugin that already started today at or after its time is left alone.
// A manual conversation keeps the slot; the analysis queue yields to the
// collection, which changes the material under it.
func (a *app) maybeAutoCollect() {
	if !a.cfg.configured() || !a.canStartCollector() {
		return
	}
	now := a.clockNow()
	today := midnight(now)
	for _, p := range a.collectorList {
		s := p.Schedule()
		if s == nil || !s.Enabled {
			continue
		}
		at, err := clockOn(today, s.Time)
		if err != nil || now.Before(at) {
			continue
		}
		state := loadCollectorDay(a.cfg.workDayDir(today))
		if run, ok := state.Items[p.ID()]; ok && startedAfter(run, at) {
			continue
		}
		if a.syncRangeCollector(p.ID(), true) {
			return
		}
		if !sameDay(a.day, today) || a.page != pageDay {
			a.selectDay(today)
			return
		}
		if a.chat.loaded {
			a.startCollector(p, true)
		}
		return
	}
}

// startedAfter reports whether a collector already ran at or after at.
func startedAfter(run collectorRun, at time.Time) bool {
	stamp := run.AutoAt
	if stamp == "" {
		stamp = run.StartedAt
	}
	if stamp == "" {
		return false
	}
	started, err := time.Parse(time.RFC3339, stamp)
	return err == nil && !started.Before(at)
}

// maybeAutoAnalyze starts every pending day once the daily analysis time
// arrives. It does not wait for the collectors: the scan only marks the
// days it changed, and this task starts whatever is marked. With nothing
// marked it does nothing and tries again the next day.
func (a *app) maybeAutoAnalyze() {
	if !a.cfg.configured() || a.analysis.Running || a.analysis.Paused {
		return
	}
	now := a.clockNow()
	today := midnight(now)
	at, err := clockOn(today, a.cfg.AutoTime)
	if err != nil || now.Before(at) {
		return
	}
	key := dayKey(today)
	if a.autoAnalyzeDay == key {
		return
	}
	// A collector or a manual conversation owns the conversation slot while
	// it runs. Wait for it instead of starting in the middle and missing
	// the slot; this is a runtime lock, not a scheduling dependency.
	if !a.chatIdle() {
		return
	}
	a.autoAnalyzeDay = key
	a.analysis.StartPending()
}

// markAutoDay records that the nightly pipeline ran, so opening the app
// again after 22:00 does not repeat it.
func (a *app) markAutoDay(day time.Time) {
	dir := a.cfg.workDayDir(day)
	state := loadCollectorDay(dir)
	state.AutoAt = time.Now().Format(time.RFC3339)
	a.saveDayState(dir, state)
	syncDayManifest(dir, day, state, a.collectorList)
	if a.chat.dir == dir {
		a.collectorDay = state
	}
	a.refreshChangedDays()
}

// markAnalyzed records the fingerprint the model just read for every
// collector of the day.
func (a *app) markAnalyzed(key string) {
	day, err := parseDayKey(key)
	if err != nil {
		return
	}
	dir := a.cfg.workDayDir(day)
	state := loadCollectorDay(dir)
	now := time.Now().Format(time.RFC3339)
	for _, p := range a.collectorList {
		run := state.Items[p.ID()]
		if run.Fingerprint != "" {
			run.AnalyzedFingerprint = run.Fingerprint
			run.AnalyzedAt = now
			state.Items[p.ID()] = run
		}
	}
	a.saveDayState(dir, state)
	syncDayManifest(dir, day, state, a.collectorList)
	if a.chat.dir == dir {
		a.collectorDay = state
	}
	a.refreshChangedDays()
}

// reloadMaterials loads the photo manifest and bitmaps after a collector
// run. Other collectors only add their own folders.
func (a *app) reloadMaterials(dayDir string) {
	if a.chat.dir != dayDir {
		return
	}
	m, err := loadManifest(dayDir)
	if err != nil {
		a.chat.manifest = nil
		a.chat.sheets = nil
		a.chat.thumbs = nil
		return
	}
	a.chat.manifest = m
	a.chat.sheets = a.decodeSheets(m)
	a.chat.thumbs = a.decodeThumbs(m)
}

// markCollector records a finished photos preparation that did not go
// through the collector queue, as openDay's first-run preparation does.
func (a *app) markCollector(id, status, errText string) {
	if a.chat.dir == "" {
		return
	}
	if a.collectorDay.Items == nil {
		a.collectorDay.Items = map[string]collectorRun{}
	}
	run := a.collectorDay.Items[id]
	run.Status = status
	run.FinishedAt = time.Now().Format(time.RFC3339)
	run.Error = errText
	if status == "done" {
		if p := a.collectorByID(id); p != nil {
			if env, err := a.collectorEnv(p, a.day, a.chat.dir); err == nil {
				run.recordFingerprint(p.Fingerprint(env))
			}
		}
	}
	a.collectorDay.Items[id] = run
	a.saveDayState(a.chat.dir, a.collectorDay)
	syncDayManifest(a.chat.dir, a.day, a.collectorDay, a.collectorList)
	a.refreshChangedDays()
}

// shortCollectProgress trims a collector's progress line for the sidebar.
func shortCollectProgress(s string) string {
	s = strings.TrimSpace(s)
	for _, pair := range [][2]string{
		{"正在从照片图库读取", "读取照片"},
		{"正在从照片库读取", "读取照片"},
		{"正在查找照片", "查找照片"},
		{"正在读取拍摄时间", "读取时间"},
		{"正在生成预览", "生成预览"},
		{"正在拼接合集", "拼接合集"},
		{"准备同步…", "准备同步"},
	} {
		s = strings.ReplaceAll(s, pair[0], pair[1])
	}
	s = strings.TrimSuffix(s, "…")
	if s == "" {
		s = "同步照片"
	}
	return s
}

func (a *app) collectTimeLabel() string {
	if strings.TrimSpace(a.cfg.CollectTime) == "" {
		return "22:00"
	}
	return a.cfg.CollectTime
}

// clockNow is the frame's clock, or the wall clock before the first frame.
func (a *app) clockNow() time.Time {
	if a.now.IsZero() {
		return time.Now()
	}
	return a.now
}

// dayHasCollectorFiles reports whether a collector folder has something in
// it; empty folders are not a day's material.
func dayHasCollectorFiles(collectors []collector, dir string) bool {
	for _, p := range collectors {
		entries, err := os.ReadDir(filepath.Join(dir, p.Folder()))
		if err == nil && len(entries) > 0 {
			return true
		}
	}
	return false
}

// flushCollectorPage persists edits the collector page made in an earlier
// build pass. It runs from the frame, once the bound controls have written
// their values.
func (a *app) flushCollectorPage() {
	if a.collectorAppDirty {
		a.collectorAppDirty = false
		a.saveCollectorPageConfig()
	}
	if a.collectorSettingsDirty {
		a.collectorSettingsDirty = false
		a.saveCollectorCfg()
	}
}

// scheduleCollectorFlush persists the page's edits after the frame that
// made them, so no file write happens inside a build.
func (a *app) scheduleCollectorFlush() {
	if a.collectorFlushQueued {
		return
	}
	a.collectorFlushQueued = true
	a.win.Update(func() {
		a.collectorFlushQueued = false
		a.flushCollectorPage()
	})
}

// saveDayState writes a day's collector state and keeps a failure for the
// settings page, instead of dropping it: an unwritable state file would
// make the next nightly run repeat the analysis.
func (a *app) saveDayState(dir string, state collectorDay) {
	if err := saveCollectorDay(dir, state); err != nil {
		a.fileErr = fmt.Errorf("保存采集状态 %s：%w", shortenPath(dir), err)
	}
}
