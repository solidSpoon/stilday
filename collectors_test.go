package main

import (
	"image/color"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"stilday/internal/analysis"
)

type fakeCollector struct {
	id     string
	folder string
	order  *[]string
}

func (f fakeCollector) ID() string          { return f.id }
func (f fakeCollector) Name() string        { return f.id }
func (f fakeCollector) Description() string { return "fake" }
func (f fakeCollector) Folder() string      { return f.folder }
func (f fakeCollector) Icon() *ui.SVG       { return iconFiles }
func (f fakeCollector) Manifest() string {
	return filepath.ToSlash(filepath.Join(f.folder, "manifest.json"))
}
func (f fakeCollector) View(c *ui.Context, view collectorView) {}
func (f fakeCollector) LoadSettings(root string) error         { return nil }
func (f fakeCollector) SaveSettings() error                    { return nil }
func (f fakeCollector) SettingsChanged() bool                  { return false }
func (f fakeCollector) KeepsFolder() bool                      { return false }
func (f fakeCollector) Schedule() *collectorSchedule {
	return &collectorSchedule{Enabled: false}
}
func (f fakeCollector) Fingerprint(env collectorEnv) string {
	return folderFingerprint{id: f.id, seed: func() string { return f.id + "-v1" }}.Fingerprint(env)
}
func (f fakeCollector) Run(env collectorEnv, progress func(string, int, int)) error {
	if f.order != nil {
		*f.order = append(*f.order, f.id)
	}
	return os.WriteFile(filepath.Join(env.Dir, f.folder, "value.txt"), []byte("ok"), 0o644)
}

func waitCollecting(t *testing.T, a *app) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !a.collecting && len(a.collectQueue) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("collector queue did not finish")
}

func TestCollectorsRunInOrderAndClearFolders(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	a.openDay(a.day)
	if err := os.MkdirAll(filepath.Join(a.chat.dir, "one"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.chat.dir, "one", "old.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	var order []string
	a.collectorList = []collector{
		fakeCollector{id: "one", folder: "one", order: &order},
		fakeCollector{id: "two", folder: "two", order: &order},
	}
	a.startAllCollectors()
	waitCollecting(t, a)
	if len(order) != 2 || order[0] != "one" || order[1] != "two" {
		t.Fatalf("order is %v", order)
	}
	if _, err := os.Stat(filepath.Join(a.chat.dir, "one", "old.txt")); !os.IsNotExist(err) {
		t.Errorf("old file survived the clear: %v", err)
	}
	for _, name := range []string{"one", "two"} {
		if _, err := os.Stat(filepath.Join(a.chat.dir, name, "value.txt")); err != nil {
			t.Errorf("%s did not run: %v", name, err)
		}
		if run := a.collectorDay.Items[name]; run.Status != "done" {
			t.Errorf("%s status is %q", name, run.Status)
		}
	}
	m, err := loadManifest(a.chat.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Collectors) != 2 || m.Collectors[0].Folder != "one" || m.Collectors[1].Folder != "two" {
		t.Fatalf("manifest collectors are %+v", m.Collectors)
	}
	st, err := os.ReadFile(collectorStatePath(a.chat.dir))
	if err != nil || len(st) == 0 {
		t.Fatalf("collector state was not written: %v", err)
	}
}

func TestCalendarStartsAt2020AndShowsAllYears(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	a.now = time.Date(2026, 10, 24, 12, 0, 0, 0, time.Local)
	a.reloadCalendar()
	var has2020, has2026, has2025Month bool
	for _, m := range a.months {
		if m.Year == 2020 && m.Month == 0 {
			has2020 = true
		}
		if m.Year == 2026 && m.Month == time.October {
			has2026 = true
		}
		if m.Year == 2025 && m.Month != 0 {
			has2025Month = true
		}
	}
	if !has2020 || !has2026 || !has2025Month {
		t.Fatalf("calendar rows are %+v", a.months)
	}
}

func TestAutoCollectRunsOncePerDay(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	src := t.TempDir()
	t.Setenv("STILDAY_PHOTOS_LIBRARY", src)
	makePhoto(t, filepath.Join(src, "a.jpg"), color.RGBA{200, 40, 40, 255})
	a.openDay(a.day)
	a.cfg.CollectTime = "22:00"
	a.now = time.Date(a.day.Year(), a.day.Month(), a.day.Day(), 22, 1, 0, 0, time.Local)
	a.page = "day"
	// The scheduled scan waits while a conversation owns the slot.
	a.chat.busy = true
	a.maybeAutoCollect()
	if a.collecting {
		t.Fatal("auto collect started during a conversation")
	}
	a.chat.busy = false
	a.maybeAutoCollect()
	waitCollecting(t, a)
	if run := a.collectorDay.Items["photos"]; run.Status != "done" {
		t.Fatalf("auto photos status is %+v", run)
	}
	if a.collectorDay.AutoAt == "" {
		t.Error("auto_at was not recorded")
	}
	a.maybeAutoCollect()
	time.Sleep(50 * time.Millisecond)
	if a.collecting {
		t.Error("auto collect ran twice")
	}
}

// The daily batch start is independent of the collectors: it starts the days
// the scan marked as changed, even when the collector's last run has not
// settled.
func TestAutoAnalyzeStartsMarkedDaysWithoutSettledCollectors(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	day := a.day
	a.openDay(day)
	dir := a.chat.dir
	if err := os.MkdirAll(filepath.Join(dir, "previews"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "previews", "001.jpg"), []byte("raw"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := loadCollectorDay(dir)
	state.Items = map[string]collectorRun{
		"photos": {Status: "running", Fingerprint: "sha256:new"},
	}
	if err := saveCollectorDay(dir, state); err != nil {
		t.Fatal(err)
	}
	a.refreshChangedDays()

	var started []string
	a.analysis = analysis.New(analysis.Hooks{
		Pending:  a.changedDaysSorted,
		StartDay: func(key string) bool { started = append(started, key); return true },
	})
	a.cfg.AutoTime = "10:00"
	a.now = time.Date(day.Year(), day.Month(), day.Day(), 10, 1, 0, 0, time.Local)
	a.page = pageDay
	// While a conversation owns the slot the batch start waits, and it
	// does not consume the day's time.
	a.chat.busy = true
	a.maybeAutoAnalyze()
	if len(started) != 0 || a.autoAnalyzeDay != "" {
		t.Fatalf("started during a conversation: %v %q", started, a.autoAnalyzeDay)
	}
	a.chat.busy = false
	a.maybeAutoAnalyze()
	if len(started) != 1 || started[0] != dayKey(day) {
		t.Fatalf("started days are %v", started)
	}
}

// With nothing marked, the daily batch start leaves the app alone.
func TestAutoAnalyzeDoesNothingWithoutPendingDays(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	a.cfg.AutoTime = "10:00"
	a.now = time.Date(a.day.Year(), a.day.Month(), a.day.Day(), 10, 1, 0, 0, time.Local)
	a.page = pageDay
	a.maybeAutoAnalyze()
	if a.analysis.Running || len(a.analysis.Queue) != 0 {
		t.Fatalf("started with nothing pending: running=%v queue=%v", a.analysis.Running, a.analysis.Queue)
	}
	if a.autoAnalyzeDay != dayKey(a.day) {
		t.Fatalf("the daily slot was not settled: %q", a.autoAnalyzeDay)
	}
}

// A manual conversation keeps the conversation slot; the analysis queue
// yields to a collector run, which changes the material under it.
func TestCollectorRunTakesTheSlotFromTheQueue(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	a.openDay(a.day)
	a.photosPlugin().value.Days = 1

	a.collecting = true
	a.sendChat("hello")
	if a.chat.busy || len(a.chat.messages) != 0 {
		t.Fatal("a message was sent while a collector was running")
	}
	a.startAnalysis()
	if a.chat.greetingPending {
		t.Fatal("an analysis started while a collector was running")
	}
	if a.canStartWork() {
		t.Fatal("work is startable while a collector runs")
	}
	a.collecting = false

	// A conversation keeps the slot.
	a.chat.busy = true
	a.runCollectorNow(photosCollectorID)
	if a.collecting {
		t.Fatal("a collector started during a conversation")
	}
	a.chat.busy = false

	// The queue yields: the run stops it before collecting.
	a.analysis.Running = true
	a.analysis.Current = dayKey(a.day)
	a.analysis.Queue = []string{dayKey(a.day.AddDate(0, 0, -1))}
	a.runCollectorNow(photosCollectorID)
	if a.analysis.Running || a.analysis.Current != "" || len(a.analysis.Queue) != 0 {
		t.Fatalf("the queue kept going: running=%v current=%q queue=%v", a.analysis.Running, a.analysis.Current, a.analysis.Queue)
	}
	if len(a.analysis.Cancelled) != 0 {
		t.Fatalf("the run cancelled days: %v", a.analysis.Cancelled)
	}

	// A manual analysis still waits for a queue that is running.
	a.analysis.Running = true
	if a.canStartWork() {
		t.Fatal("work is startable while the analysis queue runs")
	}
	a.reanalyze()
	if a.chat.greetingPending {
		t.Fatal("a manual analysis started while the queue runs")
	}
}

func TestCalendarYearJump(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	a.now = time.Date(2026, 10, 24, 12, 0, 0, 0, time.Local)
	a.calYear = 2026
	a.reloadCalendar()
	tt := ui.NewTester(a.view, 1300, 820)
	tt.Frame()
	a.jumpYear(-1)
	tt.Frame()
	if a.calYear != 2025 {
		t.Fatalf("jumped to %d", a.calYear)
	}
	a.jumpYear(1)
	tt.Frame()
	if a.calYear != 2026 {
		t.Fatalf("jumped to %d", a.calYear)
	}
}

func TestYearStepperUsesChevrons(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	a.now = time.Date(2026, 10, 24, 12, 0, 0, 0, time.Local)
	a.calYear = 2026
	a.reloadCalendar()
	a.page = "board"
	tt := ui.NewTester(a.view, 1300, 820)
	tt.Frame()
	if err := tt.Click("跳到更早的年份"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.calYear != 2025 {
		t.Fatalf("down went to %d", a.calYear)
	}
	if err := tt.Click("跳到更近的年份"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.calYear != 2026 {
		t.Fatalf("up went to %d", a.calYear)
	}
}

func TestCollectorPageShowsGlobalUI(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	a.page = "collectors"
	tt := ui.NewTester(a.view, 1440, 900)
	tt.Frame()
	for _, s := range []string{"采集器", "采集插件", "相册照片", "每日分析", "立即执行", "一天", "一周", "一个月", "三个月", "启动所有", "暂停所有", "取消所有"} {
		if !tt.HasText(s) {
			t.Errorf("missing %q", s)
		}
	}
	if tt.HasText("保存设置") {
		t.Error("the collector page still has a Save button")
	}
	// The range saves itself; there is no Save button to press.
	if err := tt.Click("一个月"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := a.photosPlugin().value.Days; got != 30 {
		t.Fatalf("the range did not save: %d", got)
	}
	// It went to the plugin's own file, not to a shared one.
	if _, err := os.Stat(filepath.Join(a.cfg.storageRoot(), "plugins", photosCollectorID, "settings.json")); err != nil {
		t.Fatalf("the plugin settings file is missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(a.cfg.storageRoot(), "collectors.json")); !os.IsNotExist(err) {
		t.Errorf("the shared collectors.json is still written: %v", err)
	}
}

func TestPhotosRangeSync(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	src := t.TempDir()
	t.Setenv("STILDAY_PHOTOS_LIBRARY", src)
	makePhoto(t, filepath.Join(src, "a.jpg"), color.RGBA{200, 40, 40, 255})
	a.openDay(a.day)
	if err := os.MkdirAll(filepath.Join(a.chat.dir, "photos"), 0o755); err != nil {
		t.Fatal(err)
	}
	a.photosPlugin().value.Days = 2
	a.syncRecentPhotoDays(false)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && a.collecting {
		time.Sleep(10 * time.Millisecond)
	}
	if a.collecting {
		t.Fatal("range sync did not finish")
	}
	if _, err := os.Stat(filepath.Join(a.chat.dir, "previews", "001.jpg")); err != nil {
		t.Fatalf("today's preview was not collected: %v", err)
	}
	if _, err := os.Stat(filepath.Join(a.chat.dir, "photos")); !os.IsNotExist(err) {
		t.Errorf("originals folder still exists: %v", err)
	}
	if run := a.collectorDay.Items["photos"]; run.Status != "done" {
		t.Fatalf("today's status is %+v", run)
	}
}

func TestCollectorFingerprintChangesWithRawFiles(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	a.openDay(a.day)
	p := newPhotosCollector()
	dir := a.chat.dir
	raw := filepath.Join(dir, p.Folder())
	if err := os.MkdirAll(raw, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(raw, "001.jpg"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := collectorEnv{Day: a.day, Dir: dir, Folder: p.Folder()}
	first := p.Fingerprint(env)
	again := p.Fingerprint(env)
	if first == "" || first != again {
		t.Fatalf("fingerprint is not stable: %q %q", first, again)
	}
	if err := os.WriteFile(filepath.Join(raw, "001.jpg"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := p.Fingerprint(env)
	if first == second {
		t.Fatal("fingerprint ignored a content change")
	}
	if err := os.WriteFile(filepath.Join(raw, "002.jpg"), []byte("three"), 0o644); err != nil {
		t.Fatal(err)
	}
	third := p.Fingerprint(env)
	if second == third {
		t.Fatal("fingerprint ignored a new file")
	}
}

func TestChangedDaysUsesAnalyzedFingerprint(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	a.openDay(a.day)
	// Fake one finished day with a fingerprint that has not been analyzed.
	dir := a.chat.dir
	if err := os.MkdirAll(filepath.Join(dir, "previews"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "previews", "001.jpg"), []byte("raw"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := loadCollectorDay(dir)
	state.Items = map[string]collectorRun{
		"photos": {Status: "done", Fingerprint: "sha256:abc"},
	}
	if err := saveCollectorDay(dir, state); err != nil {
		t.Fatal(err)
	}
	key := dayKey(a.day)
	a.refreshChangedDays()
	if !a.calendarChanged[key] {
		t.Fatalf("changed day is not pending: %v", a.calendarChanged)
	}
	state.Items["photos"] = collectorRun{Status: "done", Fingerprint: "sha256:abc", AnalyzedFingerprint: "sha256:abc"}
	if err := saveCollectorDay(dir, state); err != nil {
		t.Fatal(err)
	}
	a.refreshChangedDays()
	if a.calendarChanged[key] {
		t.Fatal("an analyzed day is still pending")
	}
}

func TestCalendarMarksUnanalyzedCollectorChanges(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	a.openDay(a.day)
	key := a.day.Format("2006-01-02")
	if err := os.MkdirAll(filepath.Join(a.chat.dir, "previews"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.chat.dir, "previews", "001.jpg"), []byte("raw"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := loadCollectorDay(a.chat.dir)
	state.Items = map[string]collectorRun{
		"photos": {Status: "done", Fingerprint: "sha256:new", AnalyzedFingerprint: "sha256:old"},
	}
	if err := saveCollectorDay(a.chat.dir, state); err != nil {
		t.Fatal(err)
	}
	a.reloadCalendar()
	if !a.calendarChanged[key] {
		t.Fatal("calendar did not mark the changed day")
	}
	state.Items["photos"] = collectorRun{Status: "done", Fingerprint: "sha256:new", AnalyzedFingerprint: "sha256:new"}
	if err := saveCollectorDay(a.chat.dir, state); err != nil {
		t.Fatal(err)
	}
	a.refreshChangedDays()
	if a.calendarChanged[key] {
		t.Fatal("calendar still marks an analyzed day")
	}
}

func TestMaterialsShowsPhotoSourceFallback(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	src := t.TempDir()
	t.Setenv("STILDAY_PHOTOS_LIBRARY", src)
	makePhoto(t, filepath.Join(src, "a.jpg"), color.RGBA{20, 40, 200, 255})
	a.openDay(a.day)
	start, end, err := a.cfg.photoWindow(a.day)
	if err != nil {
		t.Fatal(err)
	}
	m, err := preparePhotos(a.chat.dir, photoOptions{
		Source: src, Day: a.day, Start: start, End: end, MaxPhotos: 10, SheetSize: 30,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a.chat.manifest = m
	a.chat.sheets = a.decodeSheets(m)
	a.chat.thumbs = a.decodeThumbs(m)
	a.chat.manifest.PhotoSource = "originals"
	a.chat.manifest.PhotoNote = "PhotoKit 权限被拒绝，已退回 originals"
	tt := ui.NewTester(a.view, 1300, 820)
	tt.Frame()
	if !tt.HasText("PhotoKit 权限被拒绝") {
		t.Fatalf("fallback hint missing: %q", tt.Texts())
	}
}

// The collectors page lists the analysis queue, and a cancelled day stays
// visible as cancelled until it is restored.
func TestCollectorAnalysisTasks(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	a.calendarChanged["2026-10-20"] = true
	a.calendarChanged["2026-10-21"] = true
	a.analysis.Queue = []string{"2026-10-21"}
	// The rail is what this test looks at; the daily batch start has
	// already had its slot today.
	a.autoAnalyzeDay = dayKey(a.day)
	a.page = "collectors"
	tt := ui.NewTester(a.view, 1440, 900)
	tt.Frame()
	for _, want := range []string{"分析任务", "2026-10-21", "2026-10-20"} {
		if !tt.HasText(want) {
			t.Fatalf("the analysis list is missing %q: %q", want, tt.Texts())
		}
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if len(a.analysis.Cancelled) != 1 {
		t.Fatalf("cancelled %d days, want 1", len(a.analysis.Cancelled))
	}
	tt.Frame()
	if !tt.HasText("已取消") {
		t.Errorf("the cancelled day is not shown: %q", tt.Texts())
	}
	if err := tt.Click("恢复"); err != nil {
		t.Fatal(err)
	}
	if len(a.analysis.Cancelled) != 0 {
		t.Fatalf("%d days stay cancelled", len(a.analysis.Cancelled))
	}
}

// A background analysis is a small status in the settings row, out of
// the calendar's way.
func TestAnalysisStatusInSidebar(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	a.analysis.Running = true
	a.analysis.Current = "2026-10-20"
	a.analysis.Queue = []string{"2026-10-19"}
	a.page = "board"
	tt := ui.NewTester(a.view, 1440, 900)
	tt.Frame()
	if !tt.HasText("分析中 · 队列 1") {
		t.Fatalf("the sidebar has no analysis status: %q", tt.Texts())
	}
}

// The photo collector keeps what it has by default, and its settings file
// keeps every field it was given.
func TestPhotosKeepRemovedDefault(t *testing.T) {
	p := newPhotosCollector()
	if !p.value.KeepRemoved || p.value.Days != 1 {
		t.Fatalf("a fresh plugin starts at %+v", p.value)
	}
	root := t.TempDir()
	if err := p.LoadSettings(root); err != nil {
		t.Fatal(err)
	}
	p.value.KeepRemoved = false
	p.value.Days = 7
	if !p.SettingsChanged() {
		t.Fatal("an edit does not show as changed")
	}
	if err := p.SaveSettings(); err != nil {
		t.Fatal(err)
	}
	if p.SettingsChanged() {
		t.Fatal("saving did not clear the change")
	}
	again := newPhotosCollector()
	if err := again.LoadSettings(root); err != nil {
		t.Fatal(err)
	}
	if again.value.KeepRemoved || again.value.Days != 7 {
		t.Fatalf("the file reads %+v", again.value)
	}
	// A file written before a field existed starts it from the default.
	path := filepath.Join(root, "plugins", photosCollectorID, "settings.json")
	if err := os.WriteFile(path, []byte(`{"days":3}`), 0o644); err != nil {
		t.Fatal(err)
	}
	old := newPhotosCollector()
	if err := old.LoadSettings(root); err != nil {
		t.Fatal(err)
	}
	if old.value.Days != 3 || !old.value.KeepRemoved {
		t.Fatalf("an old file reads %+v", old.value)
	}
	if old.value.Schedule.Time != "22:00" {
		t.Errorf("the schedule default was lost: %+v", old.value.Schedule)
	}
}

// The switch in the photos card turns keeping removed photos off, and the
// change is written without a save button.
func TestCollectorKeepRemovedSwitch(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	a.openCollectorPage()
	tt := ui.NewTester(a.view, 1300, 900)
	tt.Frame()
	if !a.photosPlugin().value.KeepRemoved {
		t.Fatal("the switch starts off")
	}
	if err := tt.Click("保留相册里已删除的照片"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.photosPlugin().value.KeepRemoved {
		t.Fatal("clicking the switch did not turn it off")
	}
	again := newPhotosCollector()
	if err := again.LoadSettings(a.cfg.storageRoot()); err != nil {
		t.Fatal(err)
	}
	if again.value.KeepRemoved {
		t.Error("the setting was not saved")
	}
}

// The old shared collectors.json is moved into the plugin's own folder
// once, keeping what it said.
func TestCollectorSettingsMigration(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	root := a.cfg.storageRoot()
	legacy := `{
  "photos": {"days": 30, "keep_removed": false},
  "schedules": {"photos": {"enabled": true, "time": "21:30"}}
}`
	if err := os.WriteFile(filepath.Join(root, "collectors.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	a.collectorList = defaultCollectors()
	a.loadCollectorSettings()

	p := a.photosPlugin()
	if p == nil {
		t.Fatal("no photos plugin")
	}
	if p.value.Days != 30 || p.value.KeepRemoved || p.value.Schedule.Time != "21:30" {
		t.Fatalf("the migration read %+v", p.value)
	}
	if p.SettingsChanged() {
		t.Error("the migrated settings look unsaved")
	}
	if _, err := os.Stat(filepath.Join(root, "collectors.json")); !os.IsNotExist(err) {
		t.Errorf("the shared file is still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "collectors.json.migrated")); err != nil {
		t.Errorf("the old file was not kept as .migrated: %v", err)
	}
	// A second load reads the plugin's file, and the migration is done.
	a.collectorList = defaultCollectors()
	a.loadCollectorSettings()
	if again := a.photosPlugin(); again.value.Days != 30 {
		t.Fatalf("the second load read %+v", again.value)
	}
}
