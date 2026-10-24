package main

import (
	"image/color"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
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
func (f fakeCollector) FingerprintSeed() string                { return f.id + "-v1" }
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

func TestAutoAnalyzeWaitsForCollectors(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	yday := a.day.AddDate(0, 0, -1)
	a.openDay(yday)
	dir := a.chat.dir
	if err := os.MkdirAll(filepath.Join(dir, "previews"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "previews", "001.jpg"), []byte("raw"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := loadCollectorDay(dir)
	state.Items = map[string]collectorRun{
		"photos": {Status: "done", Fingerprint: "sha256:new"},
	}
	if err := saveCollectorDay(dir, state); err != nil {
		t.Fatal(err)
	}
	a.cfg.AutoTime = "10:00"
	a.now = time.Date(a.day.Year(), a.day.Month(), a.day.Day(), 10, 1, 0, 0, time.Local)
	a.page = "day"
	a.chat.piErr = "test"
	a.maybeAutoAnalyze()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && a.analysis.Running {
		time.Sleep(10 * time.Millisecond)
	}
	if a.collectorDay.Items["photos"].AnalyzedFingerprint != "" {
		t.Error("analysis succeeded without a model")
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
	for _, s := range []string{"采集器", "采集插件", "相册照片", "每日分析", "立即执行", "一天", "一周", "一个月", "三个月"} {
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
	if got := loadCollectorSettings(a.cfg.storageRoot()); got.Photos.Days != 30 {
		t.Fatalf("the range did not save: %+v", got)
	}
	// The global settings round-trip to the storage root.
	a.collectorCfg.Photos.Days = 30
	a.saveCollectorCfg()
	got := loadCollectorSettings(a.cfg.storageRoot())
	if got.Photos.Days != 30 {
		t.Fatalf("saved settings are %+v", got)
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
	a.collectorCfg.Photos.Days = 2
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
	p := photosCollector{}
	dir := a.chat.dir
	raw := filepath.Join(dir, p.Folder())
	if err := os.MkdirAll(raw, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(raw, "001.jpg"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	first := fingerprintOf(dir, p)
	again := fingerprintOf(dir, p)
	if first == "" || first != again {
		t.Fatalf("fingerprint is not stable: %q %q", first, again)
	}
	if err := os.WriteFile(filepath.Join(raw, "001.jpg"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := fingerprintOf(dir, p)
	if first == second {
		t.Fatal("fingerprint ignored a content change")
	}
	if err := os.WriteFile(filepath.Join(raw, "002.jpg"), []byte("three"), 0o644); err != nil {
		t.Fatal(err)
	}
	third := fingerprintOf(dir, p)
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
	got := a.changedDays([]time.Time{a.day})
	if len(got) != 1 || got[0] != a.day.Format("2006-01-02") {
		t.Fatalf("changed days are %v", got)
	}
	state.Items["photos"] = collectorRun{Status: "done", Fingerprint: "sha256:abc", AnalyzedFingerprint: "sha256:abc"}
	if err := saveCollectorDay(dir, state); err != nil {
		t.Fatal(err)
	}
	if got := a.changedDays([]time.Time{a.day}); len(got) != 0 {
		t.Fatalf("unchanged day is still queued: %v", got)
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
