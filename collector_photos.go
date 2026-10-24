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

// photosSettings is the photos collector's own settings, kept in
// <storage>/plugins/photos/settings.json.
type photosSettings struct {
	Days        int               `json:"days"`         // sync this many recent days
	KeepRemoved bool              `json:"keep_removed"` // keep photos the library no longer has
	Schedule    collectorSchedule `json:"schedule"`     // when it runs by itself
}

// photosCollector is the photos plugin: its settings, and the default
// fingerprint of the folder it writes.
type photosCollector struct {
	fileSettings[photosSettings]
	folderFingerprint
}

func newPhotosCollector() *photosCollector {
	p := &photosCollector{
		fileSettings: newFileSettings(photosCollectorID, photosSettings{
			Days:        1,
			KeepRemoved: true,
			Schedule:    collectorSchedule{Enabled: true, Time: "22:00"},
		}),
		folderFingerprint: folderFingerprint{id: photosCollectorID},
	}
	// Another library is another source: its material must not look like
	// the previous library's.
	p.folderFingerprint.seed = func() string { return "v1|source=" + photosLibrary() }
	return p
}

func (p *photosCollector) ID() string                   { return photosCollectorID }
func (p *photosCollector) Name() string                 { return "相册照片" }
func (p *photosCollector) Folder() string               { return "previews" }
func (p *photosCollector) Icon() *ui.SVG                { return iconPhotos }
func (p *photosCollector) Manifest() string             { return "manifest.json" }
func (p *photosCollector) Schedule() *collectorSchedule { return &p.value.Schedule }
func (p *photosCollector) Description() string {
	return "从 macOS 照片图库收集这一天的照片。"
}

// KeepsFolder keeps the day's previews when a photo the library lost
// should stay.
func (p *photosCollector) KeepsFolder() bool { return p.value.KeepRemoved }

// adoptLegacySettings takes what collectors.json said about this plugin,
// when it has no settings file of its own yet.
func (p *photosCollector) adoptLegacySettings(old legacyCollectorSettings) {
	if p.path != "" {
		if _, err := os.Stat(p.path); err == nil {
			return
		}
	}
	if old.Photos.Days > 0 {
		p.value.Days = old.Photos.Days
	}
	if old.Photos.KeepRemoved != nil {
		p.value.KeepRemoved = *old.Photos.KeepRemoved
	}
	if s := old.Schedules[p.ID()]; s != nil {
		p.value.Schedule = *s
	}
	_ = p.SaveSettings()
}

func (p *photosCollector) Run(env collectorEnv, progress func(stage string, done, total int)) error {
	src := env.Photos
	if src.Library == "" {
		return fmt.Errorf("找不到 macOS 照片图库")
	}
	_, err := preparePhotos(env.Dir, photoOptions{
		Source:      src.Library,
		Day:         env.Day,
		Start:       src.Start,
		End:         src.End,
		MaxPhotos:   src.MaxPhotos,
		SheetSize:   sheetCols * sheetRows,
		KeepRemoved: p.value.KeepRemoved,
	}, progress)
	return err
}

// View is the photos collector's settings card: the source, the quick
// recent-day range, and what happens to photos the library no longer has.
func (p *photosCollector) View(c *ui.Context, view collectorView) {
	t := c.Theme()
	const labelW = 76
	ranges := []int{1, 7, 30, 90}
	idx := 0
	for i, d := range ranges {
		if p.value.Days == d {
			idx = i
			break
		}
	}
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "同步范围").FontSize(11).TextColor(t.TextMuted).Width(labelW)
		if ui.Segmented(c, &idx, "一天", "一周", "一个月", "三个月").Label("同步最近").Changed() {
			p.value.Days = ranges[idx]
		}
	})
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "相册删除").FontSize(11).TextColor(t.TextMuted).Width(labelW)
		ui.Switch(c, &p.value.KeepRemoved).Label("保留相册里已删除的照片")
		hint := "相册里删了，这边也跟着删"
		if p.value.KeepRemoved {
			hint = "只增不删：相册里删了，这边也留着"
		}
		ui.Text(c, hint).FontSize(10.5).TextColor(t.TextMuted)
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
	if !a.cfg.configured() || !a.takeConversationSlot() {
		return
	}
	p := a.photosPlugin()
	if p == nil {
		return
	}
	days := p.value.Days
	if days < 1 {
		days = 1
	}
	now := a.clockNow()
	today := midnight(now)
	cfg := a.cfg
	keepRemoved := p.KeepsFolder()
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

			// The plugin's generated material is replaced, not merged,
			// unless the day keeps what it has: then only the derived
			// folders are rebuilt. The old photos/ folder is a leftover
			// of the previous layout either way.
			drop := []string{"photos", "sheets"}
			if !keepRemoved {
				drop = append(drop, "previews", "thumbs")
			}
			for _, name := range drop {
				_ = os.RemoveAll(filepath.Join(dir, name))
			}
			env, envErr := collectorEnvFor(cfg, p, day, dir)
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
				run.recordFingerprint(p.Fingerprint(env))
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
			a.reloadCalendar()
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
