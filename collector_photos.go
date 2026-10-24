package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo/ui"
)

// photosCollectorID is the photos plugin's identity, used where the app
// has to know that this one plugin collects a span of days.
const photosCollectorID = "photos"

type photosCollector struct{}

func (photosCollector) ID() string   { return photosCollectorID }
func (photosCollector) Name() string { return "相册照片" }
func (photosCollector) Description() string {
	return "从 macOS 照片图库收集这一天的照片。"
}
func (photosCollector) Folder() string   { return "previews" }
func (photosCollector) Icon() *ui.SVG    { return iconPhotos }
func (photosCollector) Manifest() string { return "manifest.json" }

// FingerprintSeed treats another library as a different source, so its
// material is not mistaken for the previous library's.
func (photosCollector) FingerprintSeed() string {
	return "v1|source=" + photosLibrary()
}

func (photosCollector) Run(env collectorEnv, progress func(stage string, done, total int)) error {
	src := env.Photos
	if src.Library == "" {
		return fmt.Errorf("找不到 macOS 照片图库")
	}
	_, err := preparePhotos(env.Dir, photoOptions{
		Source:    src.Library,
		Day:       env.Day,
		Start:     src.Start,
		End:       src.End,
		MaxPhotos: src.MaxPhotos,
		SheetSize: sheetCols * sheetRows,
	}, progress)
	return err
}

// View is the photos collector's global settings card: the source, the
// quick recent-day range and the date policy.
func (photosCollector) View(c *ui.Context, view collectorView) {
	t := c.Theme()
	const labelW = 76
	ranges := []int{1, 7, 30, 90}
	idx := 0
	for i, d := range ranges {
		if view.Photos.Days == d {
			idx = i
			break
		}
	}
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "同步范围").FontSize(11).TextColor(t.TextMuted).Width(labelW)
		if ui.Segmented(c, &idx, "一天", "一周", "一个月", "三个月").Label("同步最近").Changed() {
			view.Photos.Days = ranges[idx]
		}
	})
	if view.Manifest != nil && view.Manifest.PhotoNote != "" {
		ui.Row(c).Gap(10).AlignItems(ui.Start).Children(func() {
			ui.Text(c, "提示").FontSize(11).TextColor(t.TextMuted).Width(labelW)
			ui.Text(c, view.Manifest.PhotoNote).FontSize(10).TextColor(t.Warning).LineHeight(1.35)
		})
	}
}

// syncRangeCollector starts the collectors that collect a span of recent
// days rather than only the open day. Photos is the only such plugin: its
// source is a library, not a folder belonging to one day. It reports
// whether it handled the collector.
func (a *app) syncRangeCollector(id string, auto bool) bool {
	if id != photosCollectorID {
		return false
	}
	a.syncRecentPhotoDays(auto)
	return true
}

// runCollectorNow is the "run now" action of one plugin.
func (a *app) runCollectorNow(id string) {
	if a.syncRangeCollector(id, false) {
		return
	}
	a.startCollectorID(id)
}

// syncRecentPhotoDays syncs the photo collector over the configured recent
// days. Only disk work and the plugin run happen off the UI thread; the
// state the views read is published in one step at the end.
func (a *app) syncRecentPhotoDays(auto bool) {
	if !a.cfg.configured() || !a.canStartWork() {
		return
	}
	p := a.collectorByID(photosCollectorID)
	if p == nil {
		return
	}
	days := a.collectorCfg.Photos.Days
	if days < 1 {
		days = 1
	}
	now := a.clockNow()
	today := midnight(now)
	cfg := a.cfg
	collectors := a.collectorList
	selectedDir := a.chat.dir
	a.beginCollection(photosCollectorID, "准备同步…")

	sync := func() {
		var selected collectorDay
		var saveErr error
		for i := 0; i < days; i++ {
			day := today.AddDate(0, 0, -i)
			dir := cfg.workDayDir(day)
			state := loadCollectorDay(dir)
			run := state.Items[p.ID()]
			run.Status = "running"
			run.StartedAt = time.Now().Format(time.RFC3339)
			if auto {
				run.AutoAt = now.Format(time.RFC3339)
			}
			run.FinishedAt = ""
			run.Error = ""
			state.Items[p.ID()] = run
			if err := saveCollectorDay(dir, state); err != nil {
				saveErr = err
			}

			// The plugin's generated material is replaced, not merged. The
			// old photos/ folder is a leftover of the previous layout.
			for _, name := range []string{"photos", "previews", "thumbs", "sheets"} {
				_ = os.RemoveAll(filepath.Join(dir, name))
			}
			env, envErr := collectorEnvFor(cfg, day, dir)
			var runErr error
			if envErr != nil {
				runErr = envErr
			} else {
				runErr = p.Run(env, a.collectorProgress(p.ID(), day.Format("01-02")))
			}
			run.FinishedAt = time.Now().Format(time.RFC3339)
			run.Status = "done"
			if runErr != nil {
				run.Status = "error"
				run.Error = runErr.Error()
			} else {
				run.recordFingerprint(fingerprintOf(dir, p))
			}
			state.Items[p.ID()] = run
			if err := saveCollectorDay(dir, state); err != nil {
				saveErr = err
			}
			syncDayManifest(dir, day, state, collectors)
			if dir == selectedDir {
				selected = state
			}
		}
		// The scan only marks what changed: refreshChangedDays publishes
		// the pending days the task rail lists. It does not touch the
		// analysis queue, which may be running.
		publish := func() {
			a.finishCollection()
			if saveErr != nil {
				a.chat.err = "保存采集状态：" + saveErr.Error()
			}
			if selectedDir != "" && selectedDir == a.chat.dir {
				a.collectorDay = selected
				a.reloadMaterials(selectedDir)
			}
			if auto {
				a.markAutoDay(today)
			}
			a.refreshChangedDays()
		}
		publish()
	}
	if a.win == nil {
		// No window (tests): run inline so the result is immediately
		// observable and no worker mutates app state behind the caller.
		sync()
		return
	}
	go sync()
}
