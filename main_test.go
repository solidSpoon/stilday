package main

import (
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"stilday/internal/artifact"
)

// newTestApp points the app at temporary settings and photos, so tests
// never touch the real day.
func newTestApp(t *testing.T) *app {
	t.Helper()
	t.Setenv("STILDAY_HOME", t.TempDir())
	t.Setenv("STILDAY_PHOTOS_LIBRARY", t.TempDir())
	mygo.App.SetPath(mygo.PathAppData, t.TempDir())
	t.Cleanup(func() { mygo.App.SetPath(mygo.PathAppData, "") })
	return newApp()
}

// configureTestApp gives the app a temporary storage root and opens today.
func configureTestApp(t *testing.T, a *app) string {
	t.Helper()
	root := t.TempDir()
	a.cfg = defaultConfig()
	a.cfg.StorageDir = root
	if err := a.cfg.prepareStorage(); err != nil {
		t.Fatal(err)
	}
	if err := saveConfig(a.cfg); err != nil {
		t.Fatal(err)
	}
	a.store = artifact.Open(a.cfg.artifactsRoot())
	a.page = "day"
	a.day = midnight(time.Now())
	a.chat.piPath = "/usr/local/bin/pi"
	a.chat.piErr = ""
	a.loadCollectorSettings()
	a.resetForDay()
	a.loadEntries()
	a.reloadCalendar()
	a.boardDirty = true
	return root
}

// The first run asks for a storage directory, and saving it opens the
// board.
func TestFirstRunSettings(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1100, 1000)
	if a.page != "settings" {
		t.Fatalf("page %q on the first run", a.page)
	}
	if !tt.HasText("设置") || !tt.HasText("存储") {
		t.Fatalf("the settings page is missing: %q", tt.Texts())
	}
	root := t.TempDir()
	a.settingsDraft.StorageDir = root
	tt.Frame()
	if err := tt.Click("开始使用"); err != nil {
		t.Fatal(err)
	}
	if !a.cfg.configured() || a.page != "board" {
		t.Fatalf("after saving: configured=%v page=%q", a.cfg.configured(), a.page)
	}
	for _, dir := range []string{a.cfg.workRoot(), a.cfg.artifactsRoot()} {
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			t.Errorf("%s was not created: %v", dir, err)
		}
	}
}

// The global board lists every day, newest first; a card opens its day.
func TestGlobalBoard(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	yesterday := midnight(time.Now()).AddDate(0, 0, -1)
	for _, e := range []Entry{
		{OccurredAt: a.day.Add(9 * time.Hour), Day: a.day.Format("2006-01-02"), Kind: "place", Summary: "今天的咖啡"},
		{OccurredAt: yesterday.Add(20 * time.Hour), Day: yesterday.Format("2006-01-02"), Kind: "task", Summary: "昨晚的原型"},
	} {
		if err := a.store.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	a.page = "board"
	a.boardDirty = true
	tt := ui.NewTester(a.view, 1300, 820)
	tt.Frame()
	if !tt.HasText("今天的咖啡") || !tt.HasText("昨晚的原型") {
		t.Fatalf("the board is missing its events: %q", tt.Texts())
	}
	if !tt.HasText(a.day.Format("2006-01-02")) || !tt.HasText(yesterday.Format("2006-01-02")) {
		t.Errorf("the day headings are missing: %q", tt.Texts())
	}
	if len(a.boardItems) < 4 {
		t.Fatalf("%d board items", len(a.boardItems))
	}
	if err := tt.Click("昨晚的原型"); err != nil {
		t.Fatal(err)
	}
	if a.page != "day" || !sameDay(a.day, yesterday) {
		t.Fatalf("card opened page %q day %s", a.page, a.day.Format("2006-01-02"))
	}
}

// A day has its raw materials first, the events, and the chat last.
func TestDayPanels(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	a.addEntry(Entry{OccurredAt: a.day.Add(14 * time.Hour), Day: a.day.Format("2006-01-02"),
		Kind: "place", Summary: "在咖啡馆工作"})
	a.page = "day"
	tt := ui.NewTester(a.view, 1300, 820)
	tt.Frame()
	if !tt.HasText("在咖啡馆工作") {
		t.Errorf("the output timeline is missing its event: %q", tt.Texts())
	}
	if !tt.HasText("还没有照片") {
		t.Errorf("the materials panel should say there are no photos: %q", tt.Texts())
	}
}

// Clicking a calendar day opens the same day interface as 今天.
func TestCalendarSelectDay(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	yesterday := midnight(time.Now()).AddDate(0, 0, -1)
	if err := a.store.Append(Entry{
		OccurredAt: yesterday.Add(10 * time.Hour), Day: yesterday.Format("2006-01-02"),
		Kind: "observation", Summary: "yesterday",
	}); err != nil {
		t.Fatal(err)
	}
	a.reloadCalendar()
	a.boardDirty = true

	tt := ui.NewTester(a.view, 1300, 820)
	tt.Frame()
	if err := tt.Click(yesterday.Format("2006-01-02")); err != nil {
		t.Fatal(err)
	}
	if a.page != "day" || !sameDay(a.day, yesterday) {
		t.Fatalf("selected %s, page %q", a.day.Format("2006-01-02"), a.page)
	}
	if len(a.entries) != 1 || a.entries[0].Summary != "yesterday" {
		t.Fatalf("entries are %+v", a.entries)
	}
	if err := tt.Click("回到今天"); err != nil {
		t.Fatal(err)
	}
	if !sameDay(a.day, time.Now()) || a.page != "day" {
		t.Fatalf("today button: page %q day %s", a.page, a.day.Format("2006-01-02"))
	}
}

// The title bar the view draws keeps the brand clear of the window
// controls, and fills the strip they sit in.

// A day without events or a conversation shows a quiet timeline in the
// events column and nothing in the chat; analysis starts from the
// composer, not from buttons in the empty thread.
func TestDayEmptyState(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	a.page = "day"
	tt := ui.NewTester(a.view, 1300, 820)
	tt.Frame()
	if !tt.HasText("还没有事件") {
		t.Fatalf("the events placeholder is missing: %q", tt.Texts())
	}
	for _, gone := range []string{"开始分析", "开始今天", "今天有什么值得记住的"} {
		if tt.HasText(gone) {
			t.Errorf("the empty day still shows %q: %q", gone, tt.Texts())
		}
	}
	if !tt.HasText("输入栏") {
		t.Errorf("the composer is missing: %q", tt.Texts())
	}
}

// The day's layout can be reset to its default widths, and the calendar
// sidebar switches between its narrow and wide states.
func TestDayLayout(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	a.page = "day"
	a.chatWidth, a.materialsShare, a.sidebarWide = 440, 25, false
	tt := ui.NewTester(a.view, 1400, 900)
	tt.Frame()
	if err := tt.Click("重置布局"); err != nil {
		t.Fatal(err)
	}
	if a.chatWidth != defaultChatWidth || a.materialsShare != defaultMaterialsShare || !a.sidebarWide {
		t.Fatalf("reset chat %g share %g wide %v", a.chatWidth, a.materialsShare, a.sidebarWide)
	}
}

func TestSidebarWidth(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	a.page = "board"
	tt := ui.NewTester(a.view, 1400, 900)
	tt.Frame()
	if !a.sidebarWide {
		t.Fatal("the sidebar starts narrow")
	}
	if err := tt.Click("调整日历宽度"); err != nil {
		t.Fatal(err)
	}
	if a.sidebarWide {
		t.Fatal("the sidebar did not switch to narrow")
	}
	tt.Frame()
	if err := tt.Click("调整日历宽度"); err != nil {
		t.Fatal(err)
	}
	if !a.sidebarWide {
		t.Fatal("the sidebar did not switch back to wide")
	}
}

// The chat's own divider changes the chat width while the materials and
// the events share the rest.
func TestDayChatDivider(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	a.page = "day"
	tt := ui.NewTester(a.view, 1400, 900)
	tt.Frame()
	r, ok := tt.Find("调整对话宽度")
	if !ok {
		t.Fatalf("no chat divider: %q", tt.Texts())
	}
	x, y := r.X+r.W/2, float32(420)
	tt.Press(x, y)
	tt.Move(x-60, y)
	tt.Release(x-60, y)
	tt.Frame()
	if a.chatWidth <= defaultChatWidth {
		t.Fatalf("chat width is %g after dragging left", a.chatWidth)
	}
}

func TestAnalysisStartsFreshSession(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	a.page = "day"
	a.openDay(a.day)
	a.addEntry(Entry{OccurredAt: a.now, Day: a.chat.day, Kind: "task", Summary: "旧事件"})
	dir := a.cfg.workDayDir(a.day)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(dir, "chat.jsonl")
	if err := os.WriteFile(transcript, []byte(`{"role":"user","text":"旧对话"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".pi"), 0o755); err != nil {
		t.Fatal(err)
	}
	a.chat.messages = []*chatMessage{{ID: 1, Role: "user", Text: "旧对话"}}
	a.chat.nextID = 1
	a.beginAnalysisSession()
	if len(a.chat.messages) != 0 || len(a.entries) != 0 {
		t.Errorf("session not cleared: %d chat rows, %d entries", len(a.chat.messages), len(a.entries))
	}
	if _, err := os.Stat(transcript); !os.IsNotExist(err) {
		t.Errorf("transcript still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".pi")); !os.IsNotExist(err) {
		t.Errorf("Pi session still exists: %v", err)
	}
	if entries, err := a.store.Load(a.day); err != nil || len(entries) != 0 {
		t.Errorf("events after reset: %v, %v", entries, err)
	}
}

// containsFold reports whether s contains sub, ignoring case.
func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}

// A click chooses a photo in the materials panel, the arrows move the
// choice, and space opens and closes the preview over the window, where
// the arrows keep switching.
func TestPhotoPreview(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	a.page = "day"

	src := t.TempDir()
	for i := 0; i < 3; i++ {
		makePhoto(t, filepath.Join(src, "p"+string(rune('a'+i))+".jpg"), color.RGBA{uint8(80 + 40*i), 140, 200, 255})
	}
	start, end, err := a.cfg.photoWindow(a.day)
	if err != nil {
		t.Fatal(err)
	}
	dir := a.cfg.workDayDir(a.day)
	m, err := preparePhotos(dir, photoOptions{
		Source: src, Day: a.day, Start: start, End: end, MaxPhotos: 50, SheetSize: 30,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Photos) != 3 {
		t.Fatalf("%d photos prepared", len(m.Photos))
	}
	a.chat.loaded = true
	a.chat.day = a.day.Format("2006-01-02")
	a.chat.dir = dir
	a.chat.manifest = m
	a.chat.sheets = a.decodeSheets(m)
	a.chat.thumbs = a.decodeThumbs(m)
	a.chat.piPath = "/usr/local/bin/pi"

	tt := ui.NewTester(a.view, 1400, 900)
	tt.Frame()
	if err := tt.Click("大图 1"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.photos.sel != 0 {
		t.Fatalf("click chose %d", a.photos.sel)
	}

	tt.Key(0, ui.KeyRight)
	tt.Frame()
	if a.photos.sel != 1 {
		t.Fatalf("the right arrow chose %d", a.photos.sel)
	}
	// Two columns: down from the second goes to the last photo, up from
	// it to the first.
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if a.photos.sel != 2 {
		t.Fatalf("the down arrow chose %d", a.photos.sel)
	}
	tt.Key(0, ui.KeyUp)
	tt.Frame()
	if a.photos.sel != 0 {
		t.Fatalf("the up arrow chose %d", a.photos.sel)
	}

	// Preview the second photo, and switch it inside the layer.
	tt.Key(0, ui.KeyRight)
	tt.Frame()
	tt.Key(0, ui.KeySpace)
	tt.Frame()
	if !a.photos.open {
		t.Fatal("space did not open the preview")
	}
	if a.photos.cache[filepath.Join(dir, m.Photos[a.photos.sel].Preview)] == nil {
		t.Errorf("the preview was not decoded: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyLeft)
	tt.Frame()
	if a.photos.sel != 0 {
		t.Fatalf("the preview's left arrow chose %d", a.photos.sel)
	}
	tt.Key(0, ui.KeyRight)
	tt.Frame()
	if a.photos.sel != 1 {
		t.Fatalf("the preview's right arrow chose %d", a.photos.sel)
	}
	tt.Key(0, ui.KeySpace)
	tt.Frame()
	if a.photos.open {
		t.Fatal("space did not close the preview")
	}
	if !tt.HasText("大图") {
		t.Errorf("the materials panel is gone: %q", tt.Texts())
	}

	// Escape closes it too, as a dialog does.
	tt.Key(0, ui.KeySpace)
	tt.Frame()
	if !a.photos.open {
		t.Fatal("space did not reopen the preview")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.photos.open {
		t.Fatal("escape did not close the preview")
	}

	// The contact sheet a message carries previews too, and it does not
	// move the grid's choice.
	a.chat.messages = []*chatMessage{{ID: 1, Role: "user", Text: "先看今天的照片", Done: true, Images: 1}}
	a.chat.nextID = 1
	tt.Frame()
	if err := tt.Click("今日合集"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.photos.open {
		t.Fatal("clicking the sheet did not open the preview")
	}
	if a.photos.fromPhotos || a.photos.sel != 1 || a.photos.index != 0 {
		t.Fatalf("the sheet preview moved the grid: fromPhotos=%v sel=%d index=%d",
			a.photos.fromPhotos, a.photos.sel, a.photos.index)
	}
	if a.photos.cache[filepath.Join(dir, m.Sheets[0].File)] == nil {
		t.Errorf("the sheet was not decoded")
	}
	tt.Key(0, ui.KeySpace)
	tt.Frame()
	if a.photos.open {
		t.Fatal("space did not close the sheet preview")
	}
}

// The settings page offers starting at login, next to what closing the
// window does.
func TestSettingsLaunchGroup(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	a.openSettings()
	tt := ui.NewTester(a.view, 1200, 1000)
	tt.Frame()
	for _, s := range []string{"启动与后台", "开机自动启动"} {
		if !tt.HasText(s) {
			t.Errorf("the settings page is missing %q", s)
		}
	}
}

// Opening a day only looks: no work folder, no calendar mark. The folder
// appears when something is written into it.
func TestOpeningADayLeavesNoFolder(t *testing.T) {
	a := newTestApp(t)
	configureTestApp(t, a)
	empty := a.day.AddDate(0, 0, -3)
	a.openDay(empty)
	if _, err := os.Stat(a.cfg.workDayDir(empty)); !os.IsNotExist(err) {
		t.Fatal("opening an empty day created its work folder")
	}
	if a.dataDays[dayKey(empty)] {
		t.Fatal("opening an empty day marked it as having data")
	}
	// A message creates the folder, and only then.
	a.addMessage(&chatMessage{Role: "user", Text: "记一笔"})
	if _, err := os.Stat(filepath.Join(a.cfg.workDayDir(empty), "chat.jsonl")); err != nil {
		t.Fatal("a saved message did not create the day's transcript:", err)
	}
	// An entry marks its day.
	a.openDay(a.day)
	a.addEntry(artifact.Entry{OccurredAt: time.Now(), Kind: "observation", Summary: "记一笔"})
	if !a.dataDays[dayKey(a.day)] {
		t.Fatal("a saved entry did not mark its day")
	}
}
