package main

import (
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"stilday/internal/artifact"
	"stilday/internal/schedule"
)

// pendingTurn is one user message waiting for the one Pi turn slot. The
// message is already in its conversation; only the prompt is waiting.
type pendingTurn struct {
	state  *chatState
	text   string
	images int
}

// app is the window and the state every page reads: the selected day, its
// entries, the calendar and board caches, and the long-lived machines — the
// days' chats, the collector runs and the analysis scheduler.
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

	// mcp is the local query endpoint other assistants connect to.
	mcp mcpService

	// The global board, all days newest first.
	boardEntries []Entry
	boardItems   []boardItem
	boardQuery   string
	boardLoaded  bool
	boardDirty   bool
	boardList    ui.ListState

	// chats holds every day's conversation this session has opened or
	// analyzed, keyed by YYYY-MM-DD. chat points at the selected day's
	// state; the page always renders that one. A day's agent keeps running
	// while another day is selected.
	chats map[string]*chatState
	chat  *chatState
	// turn is the conversation holding the one Pi turn slot, whether a
	// user's message or the day's analysis. pending holds user messages
	// accepted while the slot was busy; they run before the next analysis.
	turn    *chatState
	pending []pendingTurn

	piPath string
	piErr  string
	pi     piState
	photos photoView

	calendar        ui.ListState
	months          []month
	dataDays        map[string]bool
	calendarChanged map[string]bool
	calYear         int

	settingsDraft  config
	settingsErr    string
	keyDraft       string
	catchUpDraft   string
	collectStarted time.Time

	// The task page edits drafts; nothing is applied until its card's save.
	autoDraftOn     bool
	autoDraftDays   []int
	autoDraftCustom bool
	autoDraftTime   string
	autoDraftTimes  []string
	autoDraftReady  bool
	pluginDrafts    map[string]collectorSchedule
	keySaved        bool
	modelPickerOpen bool
	modelQuery      string
	models          []modelInfo
	modelsErr       string
	modelsLoading   bool

	// Collectors: the registered plugins, the selected day's status, and
	// the queue the nightly pipeline runs in order.
	collectorList []collector
	collectorDay  collectorDay
	// Edits on the collector page are persisted on the next frame, never
	// in the middle of the build that made them.
	collectorAppDirty      bool
	collectorSettingsDirty bool
	collectorFlushQueued   bool
	collecting             bool
	collectID              string
	collectProgress        string
	collectQueue           []string

	// The daily scheduler owns the per-day analysis tasks and the storage
	// its records belong to.
	sched     *schedule.Scheduler
	schedRoot string

	copiedUntil time.Time
	fileErr     error
	now         time.Time

	// The menu bar icon, whether the app starts at login, and the flags
	// that tell a quit from a window the user closed.
	tray           *mygo.Tray
	autoStart      bool
	autoStartDraft bool
	quitting       bool
	stopped        chan struct{}
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
		chats:           map[string]*chatState{},
		collectorList:   defaultCollectors(),
		collectorDay:    collectorDay{Items: map[string]collectorRun{}},
		stopped:         make(chan struct{}),
	}
	a.cfg, a.cfgErr = loadConfig()
	a.store = artifact.Open(a.cfg.artifactsRoot())
	if a.cfg.configured() {
		a.loadCollectorSettings()
		a.pruneContentlessDays()
		go cleanupLegacyPhotoOriginals(a.cfg.workRoot())
	}
	a.chat = newChatState()
	a.resetPhotos()
	a.pi = piState{version: piVersion, path: installedPiBinary()}
	a.pi.installed = a.pi.path != ""
	a.piPath, a.piErr = resolvePi()
	if !a.cfg.configured() {
		a.page = pageSettings
		a.settingsDraft = a.cfg
		a.settingsDraft.StorageDir = suggestStorageDir()
	} else {
		a.loadEntries()
	}
	a.reloadCalendar()
	a.syncAnalysisTasks()
	return a
}

// view builds the window: the sidebar and the page it chose, every frame.
func (a *app) view(c *ui.Context) {
	c.SetTheme(appleTheme(c.Theme()))
	a.now = time.Now()
	a.tick()
	// The preview belongs to the day's page: leaving it puts the
	// preview away, so that coming back does not reopen it.
	if a.page != pageDay {
		a.photos.open = false
	}
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
	if s := a.scheduler(); s != nil {
		s.Tick()
	}
}

// The conversation has one turn slot: a Pi turn, the opening line, the
// photo preparation and a collector run all write chat state (busy,
// preparing, progress). These predicates are the one place the entry
// points ask whether that slot is free, so a manual action, a collector and
// a scheduled task can never claim it twice.

// chatIdle reports whether the conversation slot is free.
func (a *app) chatIdle() bool {
	if a.collecting || a.turn != nil {
		return false
	}
	if a.chat != nil && a.chat.preparing {
		return false
	}
	for _, s := range a.chats {
		if s.preparing {
			return false
		}
	}
	return true
}

// canStartWork reports whether a fresh analysis or collector run may begin.
func (a *app) canStartWork() bool {
	return a.chatIdle()
}

// canStartAnalysis reports whether the rail's start action has a waiting
// day. Making a task due is safe while another turn runs: it starts when
// the slot frees.
func (a *app) canStartAnalysis() bool {
	s := a.scheduler()
	return s != nil && s.CanStart()
}

// canStartCollector reports whether a collector run can take the slot: it is
// idle, or only an analysis is using it, which a run stops.
func (a *app) canStartCollector() bool {
	return !a.collecting && (a.chatIdle() || a.analysisRunningDay() != "")
}

// takeConversationSlot stops the running analysis, whose material a
// collector run is about to change, and reports whether the run may start.
// A manual conversation keeps the slot.
func (a *app) takeConversationSlot() bool {
	if a.collecting {
		return false
	}
	if day := a.analysisRunningDay(); day != "" {
		a.interruptAnalysis(day)
		return true
	}
	return a.chatIdle()
}

// selectDay switches the whole app to another day. Days keep their
// conversations and their agents: switching away from a busy day does not
// stop it.
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
	a.resetPhotos()
	a.openDay(day)
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

// appendEntry writes an event to a day that is not the selected one,
// without touching the selected day's in-memory timeline.
func (a *app) appendEntry(e Entry) {
	if e.Day == "" {
		return
	}
	if e.UpdatedAt.IsZero() {
		e.UpdatedAt = time.Now()
	}
	a.dataDays[e.Day] = true
	a.boardDirty = true
	if err := a.store.Append(e); err != nil {
		a.fileErr = err
	}
}
