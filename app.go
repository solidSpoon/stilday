package main

import (
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"stilday/internal/analysis"
	"stilday/internal/artifact"
)

// app is the window and the state every page reads: the selected day, its
// entries, the calendar and board caches, and the three long-lived
// machines — the chat, the collector runs and the analysis queue.
type app struct {
	win *mygo.Window

	cfg    config
	cfgErr error
	store  *artifact.Store

	day  time.Time // the selected day, at local midnight
	page pageID    // which destination the sidebar opened

	entries     []Entry
	seq         int64
	outputFocus int64

	// Day layout: the chat keeps a width of its own, and the materials and
	// the events share the rest, dragged freely. The calendar sidebar has
	// a narrow and a wide state.
	chatWidth      float32
	materialsShare float32
	sidebarWide    bool

	// The global board, all days newest first.
	boardEntries []Entry
	boardItems   []boardItem
	boardQuery   string
	boardLoaded  bool
	boardDirty   bool
	boardList    ui.ListState

	chat chatState
	pi   piState

	calendar        ui.ListState
	months          []month
	dataDays        map[string]bool
	calendarChanged map[string]bool
	calYear         int

	settingsDraft   config
	settingsErr     string
	keyDraft        string
	keySaved        bool
	keySaveTimer    *time.Timer
	modelPickerOpen bool
	modelQuery      string
	models          []modelInfo
	modelsErr       string
	modelsLoading   bool

	// Collectors: the registered plugins, the selected day's status, and
	// the queue the nightly pipeline runs in order.
	collectorList []collector
	collectorCfg  collectorSettings
	collectorDay  collectorDay
	// Edits on the collector page are persisted on the next frame, never
	// in the middle of the build that made them.
	collectorAppDirty    bool
	collectorCfgDirty    bool
	collectorFlushQueued bool
	collecting           bool
	collectID            string
	collectProgress      string
	collectQueue         []string
	autoAnalyzeDay       string

	// The background analysis: which day is running, what waits behind it,
	// and what the user cancelled.
	analysis analysis.Queue

	copiedUntil time.Time
	fileErr     error
	now         time.Time
}

// pageID names a destination in the sidebar.
type pageID string

const (
	pageBoard      pageID = "board"
	pageDay        pageID = "day"
	pageSettings   pageID = "settings"
	pageCollectors pageID = "collectors"
)

// newApp loads the settings, then the selected (today's) day.
func newApp() *app {
	a := &app{
		now:             time.Now(),
		day:             midnight(time.Now()),
		page:            pageBoard,
		dataDays:        map[string]bool{},
		calendarChanged: map[string]bool{},
		calYear:         time.Now().Year(),
		boardDirty:      true,
		chatWidth:       defaultChatWidth,
		materialsShare:  defaultMaterialsShare,
		sidebarWide:     true,
		collectorList:   defaultCollectors(),
		collectorCfg:    defaultCollectorSettings(),
		collectorDay:    collectorDay{Items: map[string]collectorRun{}},
	}
	a.cfg, a.cfgErr = loadConfig()
	a.store = artifact.Open(a.cfg.artifactsRoot())
	if a.cfg.configured() {
		a.collectorCfg = loadCollectorSettings(a.cfg.storageRoot())
		go cleanupLegacyPhotoOriginals(a.cfg.workRoot())
	}
	a.chat = chatState{
		byCall: map[string]*chatMessage{},
	}
	a.pi = piState{version: piVersion, path: installedPiBinary()}
	a.pi.installed = a.pi.path != ""
	a.chat.piPath, a.chat.piErr = resolvePi()
	if !a.cfg.configured() {
		a.page = pageSettings
		a.settingsDraft = a.cfg
		a.settingsDraft.StorageDir = suggestStorageDir()
	} else {
		a.loadEntries()
	}
	a.wireAnalysis()
	a.reloadCalendar()
	return a
}

// view builds the window: the sidebar and the page it chose, every frame.
func (a *app) view(c *ui.Context) {
	c.SetTheme(appleTheme(c.Theme()))
	a.now = time.Now()
	a.tick()
	if a.cfg.configured() {
		if !a.hasMonth(a.now.Year(), a.now.Month()) {
			a.reloadCalendar()
		}
	}
	c.After(time.Second)

	// The sidebar is native material: the root and the sidebar draw no
	// background where the window has vibrancy. Elsewhere they fall back to
	// the theme's flat surfaces.
	vibrant := c.Vibrancy()
	if vibrant {
		c.Root().Background(ui.Transparent)
	}
	root := ui.Row(c).Fill().AlignItems(ui.Stretch)
	if !vibrant {
		root.Background(c.Theme().Background)
	}
	root.Children(func() {
		a.sidebar(c, vibrant)
		ui.Column(c).Fill().Grow(1).MinWidth(0).Background(c.Theme().Background).Children(func() {
			switch a.page {
			case pageSettings:
				a.settingsPage(c)
			case pageDay:
				a.dayPage(c)
			case pageCollectors:
				a.collectorPage(c)
			default:
				a.boardPage(c)
			}
		})
	})
}

// tick advances everything that follows the clock: the collector and
// analysis schedules, and the edits a page left for the next pass. Each
// step guards on its own state, so a frame that changes nothing writes
// nothing.
func (a *app) tick() {
	a.flushCollectorPage()
	a.maybeAutoCollect()
	a.maybeAutoAnalyze()
}

// The conversation has one turn slot: a Pi turn, the opening line, the
// photo preparation and a collector run all write the same chat state
// (busy, preparing, progress). These two predicates are the one place the
// entry points ask whether that slot is free, so a manual action, a
// collector and a scheduled task can never claim it twice.

// chatIdle reports whether the conversation slot is free.
func (a *app) chatIdle() bool {
	return !a.chat.busy && !a.chat.preparing && !a.chat.greetingPending && !a.collecting
}

// canStartWork reports whether a fresh analysis or collector run may begin.
// The analysis queue starts its own days through startAnalysis, so that
// path asks chatIdle instead: it is allowed to run while the queue itself
// is the owner.
func (a *app) canStartWork() bool {
	return a.chatIdle() && !a.analysis.Running
}

// selectDay switches the whole app to another day.
func (a *app) selectDay(day time.Time) {
	if !a.cfg.configured() {
		a.openSettings()
		return
	}
	day = midnight(day)
	a.day = day
	a.page = pageDay
	a.outputFocus = 0
	a.calYear = day.Year()
	a.rebuildCalendarRows()
	a.loadEntries()
	a.resetForDay()
}

// loadEntries reads the selected day's artifact.
func (a *app) loadEntries() {
	entries, err := a.store.Load(a.day)
	a.fileErr = err
	a.entries = entries
	for i := range a.entries {
		a.entries[i].Seq = int64(i + 1)
	}
	a.seq = int64(len(a.entries))
}

// addEntry puts an event into the selected day and its artifact.
func (a *app) addEntry(e Entry) {
	a.seq++
	e.Seq = a.seq
	if e.Day == "" {
		e.Day = dayKey(a.day)
	}
	if e.UpdatedAt.IsZero() {
		e.UpdatedAt = time.Now()
	}
	a.entries = append(a.entries, e)
	a.dataDays[dayKey(a.day)] = true
	a.boardDirty = true
	if err := a.store.Append(e); err != nil {
		a.fileErr = err
	}
}
