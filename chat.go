package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"stilday/agent"
)

// chatMessage is one row of the conversation. Tool runs are rows too, so
// the user can see what the model looked at.
type chatMessage struct {
	ID         int
	Role       string // user, assistant, tool
	Text       string
	Done       bool
	Tool       string
	CallID     string
	ToolStatus string
	ToolDetail string
	Images     int
	TS         time.Time
}

// chatRow is one rendered row: a message, or the turn's tool calls
// collapsed into one compact line.
type chatRow struct {
	key     any
	msg     *chatMessage
	tools   []*chatMessage
	pending bool
}

// chatState is one day's conversation, its Pi process, and the photos the
// model is looking at. Every day the app has opened or analyzed keeps its
// own state, so switching days does not stop a turn.
type chatState struct {
	loaded   bool
	day      string // YYYY-MM-DD
	date     time.Time
	dir      string
	messages []*chatMessage
	byCall   map[string]*chatMessage
	list     ui.ListState
	md       markdown.Options
	input    string

	busy      bool
	preparing bool
	prepSeq   int
	progress  string
	err       string
	forcePrep bool
	// analysis marks the turn as the scheduler's analysis job, which
	// reports its outcome when the turn settles.
	analysis bool

	manifest        *manifest
	sheets          []*ui.Bitmap
	thumbs          []*ui.Bitmap
	client          *agent.Client
	ctx             context.Context
	cancel          context.CancelFunc
	nextID          int
	streaming       *chatMessage
	sheetsSent      bool
	systemOpen      bool
	greetingPending bool
}

// newChatState is the empty conversation before any day is opened.
func newChatState() *chatState {
	return &chatState{byCall: map[string]*chatMessage{}}
}

// dayState returns a day's conversation, loading its transcript and
// manifest from disk the first time it is asked for. It does not select
// the day and does not prepare anything.
func (a *app) dayState(day time.Time) *chatState {
	key := dayKey(day)
	if s, ok := a.chats[key]; ok {
		return s
	}
	s := newChatState()
	s.loaded = true
	s.day = key
	s.date = midnight(day)
	if a.cfg.configured() {
		s.dir = a.cfg.workDayDir(day)
	}
	if s.dir == "" {
		a.chats[key] = s
		return s
	}
	// Opening a day only looks at it: the work folder appears when
	// something is written into it, so a visit leaves no empty folder
	// behind (and the calendar has nothing to mark).
	s.messages = loadTranscript(s.dir)
	for _, m := range s.messages {
		s.nextID++
		m.ID = s.nextID
		if m.Images > 0 || m.Role == "assistant" {
			s.sheetsSent = true
		}
	}
	if m, err := loadManifest(s.dir); err == nil {
		s.manifest = m
		s.sheets = a.decodeSheets(s, m)
		s.thumbs = a.decodeThumbs(s, m)
	}
	a.chats[key] = s
	return s
}

// openDay selects a day, loading its conversation, collector state and
// manifest. Other days keep their state and their agents.
func (a *app) openDay(day time.Time) {
	key := dayKey(day)
	switched := a.chat == nil || a.chat.day != key
	a.chat = a.dayState(day)
	if switched {
		a.collectorDay = loadCollectorDay(a.chat.dir)
	}
	// Opening a day only reads what the collectors already wrote: the
	// library is read by the scheduled run, not by a page.
	if a.chat.forcePrep {
		a.chat.forcePrep = false
		a.chat.manifest = nil
		a.chat.sheets = nil
		a.chat.thumbs = nil
	}
	if a.chat.manifest == nil && a.chat.dir != "" {
		a.reloadMaterials(a.chat.dir)
	}
}

// resetForDay drops the selected day's cached conversation so the next
// openDay loads it from disk again. Other days keep theirs.
func (a *app) resetForDay() {
	if a.chat != nil {
		a.chat.prepSeq++
		a.closeAgent(a.chat)
		if a.chat.day != "" && a.chats[a.chat.day] == a.chat {
			delete(a.chats, a.chat.day)
		}
		if a.turn == a.chat {
			a.turn = nil
		}
	}
	a.chat = newChatState()
	a.resetPhotos()
}

// resetAllDays closes every day's agent and forgets the cached
// conversations, as a storage change requires.
func (a *app) resetAllDays() {
	a.closeAllAgents()
	// Interrupt on the scheduler that existed before the setting change:
	// asking for a scheduler here would build the new storage's one and
	// sync it against the old root's changed days.
	if a.sched != nil {
		if task, ok := a.sched.RunningTask(); ok {
			a.sched.Interrupt(task.ID, a.analysisDue(a.clockNow()))
		}
	}
	a.chats = map[string]*chatState{}
	a.turn = nil
	a.pending = nil
	a.chat = newChatState()
	a.resetPhotos()
}

// closeAllAgents closes every day's Pi child, as a storage change or a quit
// needs.
func (a *app) closeAllAgents() {
	for _, s := range a.chats {
		s.prepSeq++
		a.closeAgent(s)
	}
	if a.chat != nil && a.chats[a.chat.day] != a.chat {
		a.chat.prepSeq++
		a.closeAgent(a.chat)
	}
}

// clearMessages drops the transcript rows, the streaming target and the
// list position, keeping the day and its material.
func (s *chatState) clearMessages() {
	s.messages = nil
	s.byCall = map[string]*chatMessage{}
	s.streaming = nil
	s.nextID = 0
	s.list = ui.ListState{}
}

// dayOf returns a state's day as a local midnight.
func (s *chatState) dayOf() time.Time {
	if !s.date.IsZero() {
		return s.date
	}
	if d, err := parseDayKey(s.day); err == nil {
		return d
	}
	return time.Time{}
}

// decodeSheets loads the contact sheets, keeping one slot per manifest
// sheet so a picture still knows which sheet it is.
func (a *app) decodeSheets(s *chatState, m *manifest) []*ui.Bitmap {
	out := make([]*ui.Bitmap, len(m.Sheets))
	for i, sh := range m.Sheets {
		data, err := os.ReadFile(filepath.Join(s.dir, sh.File))
		if err != nil {
			continue
		}
		if b, err := ui.DecodeBitmap(data); err == nil {
			out[i] = b
		}
	}
	return out
}

// decodeThumbs loads the 资料 panel's thumbnails. Manifests written before
// thumbs existed fall back to the previews for small days; larger old days
// stay a list until the collector rewrites them.
func (a *app) decodeThumbs(s *chatState, m *manifest) []*ui.Bitmap {
	out := make([]*ui.Bitmap, len(m.Photos))
	for i, p := range m.Photos {
		rel := p.Thumb
		if rel == "" && len(m.Photos) <= 12 {
			rel = p.Preview
		}
		if rel == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, rel))
		if err != nil {
			continue
		}
		b, err := ui.DecodeBitmap(data)
		if err == nil {
			out[i] = b
		}
	}
	return out
}

// chatHeader is the day's small contact card above the thread: an avatar,
// the assistant's name, and a glass button that opens the prompt.
func (a *app) chatHeader(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Absolute().Top(8).Left(0).Right(0).AlignItems(ui.Center).Children(func() {
		// The whole invisible card is clickable; the pill is the lower
		// layer and the avatar covers its top.
		card := ui.Column(c).Padding(8, 10).Radius(20).Gap(0).AlignItems(ui.Center).
			Cursor(ui.CursorPointer).Label("系统提示").Tooltip("查看系统提示")
		card.Children(func() {
			ui.Box(c).Width(160).Height(77).Children(func() {
				ui.Column(c).Absolute().Top(46).Left(0).Right(0).AlignItems(ui.Center).
					Children(func() {
						pill := ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(7, 16).Radius(18).
							Material(glass.Glass{Tint: t.Text.Alpha(0.07)})
						pill.Children(func() {
							ui.Text(c, "Stilday").FontSize(13).Bold().TextColor(t.Text)
							ui.Icon(c, iconChevron).FontSize(12).TextColor(t.TextMuted)
						})
					})
				ui.Column(c).Absolute().Top(0).Left(0).Right(0).AlignItems(ui.Center).
					Children(func() {
						avatar := ui.Box(c).Size(52, 52).Radius(26).Center().
							Gradient(ui.Hex("#A9BDF0"), ui.Hex("#7F97D8"), 145).
							Border(1, t.AccentText.Alpha(0.35))
						avatar.Children(func() {
							ui.Icon(c, iconPerson).FontSize(26).TextColor(t.AccentText)
						})
					})
			})
		})
		if card.Clicked() {
			a.chat.systemOpen = true
		}
	})
}

// promptForDrawer returns the stored system prompt, or the current one when
// the conversation predates the prompt the app ships now.
func (a *app) promptForDrawer() string {
	text := a.latestSystemPrompt()
	// The stored prompt is the base it was generated from, then that day's
	// context. A different base means the conversation predates a prompt
	// change: show the current one rather than the stale copy.
	if text == "" || !strings.HasPrefix(text, strings.TrimSpace(promptBase())) {
		return a.systemPrompt()
	}
	return text
}

// systemPromptModal is the centered dialog with the system prompt.
func (a *app) systemPromptModal(c *ui.Context) {
	ui.DialogBase(c, &a.chat.systemOpen, func(backdrop, panel ui.Element) {
		t := c.Theme()
		backdrop.Background(t.Text.Alpha(0.18))
		panel.Width(720).Height(560).Padding(20).Radius(16).
			Background(t.Surface).Border(1, t.Border.Alpha(0.4))
		panel.Children(func() {
			ui.Column(c).FillHeight().Gap(12).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "系统提示").FontSize(15).Bold().TextColor(t.Text)
					ui.Spacer(c)
					if ui.Button(c, "关闭").FontSize(11).Padding(3, 8).Radius(6).Clicked() {
						a.chat.systemOpen = false
					}
				})
				text := a.promptForDrawer()
				ui.Scroll(c).Grow(1).Children(func() {
					ui.Text(c, text).FontSize(11.5).TextColor(t.TextMuted).Selectable().LineHeight(1.5)
				})
			})
		})
	})
}

func (a *app) latestSystemPrompt() string {
	for i := len(a.chat.messages) - 1; i >= 0; i-- {
		if a.chat.messages[i].Role == "system" {
			return a.chat.messages[i].Text
		}
	}
	return ""
}

func (a *app) chatMessages(c *ui.Context) {
	rows := a.chatRows()
	a.chat.list.Key = func(i int) any { return rows[i].key }
	a.chat.list.FollowEnd = true
	a.chat.list.FollowAnimation = &ui.Smooth
	ui.List(c, &a.chat.list, len(rows), func(i int) {
		a.chatRow(c, rows[i])
	}).Grow(1).Padding(20, 20, 16).Gap(18).Justify(ui.End)
}
