package main

import (
	"context"
	"fmt"
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

// chatRow is one rendered row: a message, or the turn's tool calls
// collapsed into one compact line.
type chatRow struct {
	key     any
	msg     *chatMessage
	tools   []*chatMessage
	pending bool
}

// chatState is the day's conversation, its Pi process, and the photos the
// model is looking at.

// chatState is the day's conversation, its Pi process, and the photos the
// model is looking at.
type chatState struct {
	loaded   bool
	day      string
	dir      string
	messages []*chatMessage
	byCall   map[string]*chatMessage
	list     ui.ListState
	md       markdown.Options
	input    string

	busy        bool
	preparing   bool
	prepSeq     int
	progress    string
	err         string
	forcePrep   bool
	autoAnalyze bool

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

	piPath string
	piErr  string
}

// openDay switches the chat to a day, closing the previous Pi process.

// openDay switches the chat to a day, closing the previous Pi process.
func (a *app) openDay(day time.Time) {
	key := dayKey(day)
	dir := ""
	if a.cfg.configured() {
		dir = a.cfg.workDayDir(day)
	}
	if a.chat.loaded && a.chat.day == key && a.chat.dir == dir {
		return
	}
	a.resetForDay()
	a.chat.loaded = true
	a.chat.day = key
	a.chat.dir = dir
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		a.chat.err = "创建工作目录：" + err.Error()
		return
	}
	a.dataDays[key] = true
	a.chat.messages = loadTranscript(dir)
	for _, m := range a.chat.messages {
		a.chat.nextID++
		m.ID = a.chat.nextID
		if m.Images > 0 || m.Role == "assistant" {
			a.chat.sheetsSent = true
		}
	}
	a.collectorDay = loadCollectorDay(dir)
	if m, err := loadManifest(dir); err == nil {
		a.chat.manifest = m
		a.chat.sheets = a.decodeSheets(m)
		a.chat.thumbs = a.decodeThumbs(m)
	}
	if a.chat.forcePrep && a.win != nil {
		a.chat.forcePrep = false
		a.prepareDay(true)
	} else if a.chat.manifest == nil && photosLibrary() != "" && a.win != nil {
		a.prepareDay(false)
	}
}

// resetForDay drops the chat state so the next openDay loads the selected
// day from disk.

// resetForDay drops the chat state so the next openDay loads the selected
// day from disk.
func (a *app) resetForDay() {
	a.closeAgent()
	a.chat.reset()
	a.resetPhotos()
}

// reset returns the conversation to its pre-open state. The day's folder,
// its photos and its manifest are loaded again by openDay.

// reset returns the conversation to its pre-open state. The day's folder,
// its photos and its manifest are loaded again by openDay.
func (s *chatState) reset() {
	s.loaded = false
	s.day = ""
	s.dir = ""
	s.clearMessages()
	s.manifest = nil
	s.sheets = nil
	s.thumbs = nil
	s.busy = false
	s.preparing = false
	s.prepSeq++
	s.progress = ""
	s.err = ""
	s.forcePrep = false
	s.autoAnalyze = false
	s.systemOpen = false
	s.greetingPending = false
}

// clearMessages drops the transcript rows, the streaming target and the
// list position, keeping the day and its material.

// clearMessages drops the transcript rows, the streaming target and the
// list position, keeping the day and its material.
func (s *chatState) clearMessages() {
	s.messages = nil
	s.byCall = map[string]*chatMessage{}
	s.streaming = nil
	s.nextID = 0
	s.list = ui.ListState{}
}

// prepareDay copies the day's photos, builds the numbered contact sheets,
// and writes the manifest. It runs off the main thread and reports back
// through Window.Update.
func (a *app) prepareDay(force bool) {
	if a.chat.preparing || !a.cfg.configured() || a.chat.dir == "" {
		return
	}
	if !force && a.chat.manifest != nil {
		return
	}
	library := photosLibrary()
	if library == "" {
		a.chat.err = "找不到 macOS 照片图库。"
		a.chat.autoAnalyze = false
		return
	}
	start, end, err := a.cfg.photoWindow(a.day)
	if err != nil {
		a.chat.err = err.Error()
		a.chat.autoAnalyze = false
		return
	}
	dir := a.chat.dir
	cfg := a.cfg
	day := a.day
	a.chat.preparing = true
	a.chat.prepSeq++
	seq := a.chat.prepSeq
	a.chat.progress = "正在整理照片…"
	opts := photoOptions{
		Source:    library,
		Day:       day,
		Start:     start,
		End:       end,
		MaxPhotos: cfg.MaxPhotos,
		SheetSize: sheetCols * sheetRows,
	}
	go func() {
		m, err := preparePhotos(dir, opts, func(stage string, done, total int) {
			if a.win == nil {
				return
			}
			text := stage
			if total > 0 {
				text = fmt.Sprintf("%s %d/%d", stage, done, total)
			}
			a.win.Update(func() {
				if a.chat.prepSeq != seq {
					return
				}
				a.chat.progress = text
			})
		})
		if a.win == nil {
			return
		}
		a.win.Update(func() {
			if a.chat.prepSeq != seq {
				return
			}
			a.chat.preparing = false
			if err != nil {
				a.chat.err = "照片整理失败：" + err.Error()
				a.chat.manifest = nil
				a.chat.sheets = nil
				a.chat.thumbs = nil
				a.chat.autoAnalyze = false
				a.markCollector(photosCollectorID, "error", err.Error())
				return
			}
			a.chat.manifest = m
			a.chat.sheets = a.decodeSheets(m)
			a.chat.thumbs = a.decodeThumbs(m)
			a.chat.err = ""
			a.markCollector(photosCollectorID, "done", "")
			if a.chat.autoAnalyze {
				a.chat.autoAnalyze = false
				a.startAnalysis()
			}
		})
	}()
}

// decodeSheets loads the contact sheets, keeping one slot per manifest
// sheet so a picture still knows which sheet it is.
func (a *app) decodeSheets(m *manifest) []*ui.Bitmap {
	out := make([]*ui.Bitmap, len(m.Sheets))
	for i, s := range m.Sheets {
		data, err := os.ReadFile(filepath.Join(a.chat.dir, s.File))
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

// decodeThumbs loads the 资料 panel's thumbnails. Manifests written before
// thumbs existed fall back to the previews for small days; larger old days
// stay a list until the collector rewrites them.
func (a *app) decodeThumbs(m *manifest) []*ui.Bitmap {
	out := make([]*ui.Bitmap, len(m.Photos))
	for i, p := range m.Photos {
		rel := p.Thumb
		if rel == "" && len(m.Photos) <= 12 {
			rel = p.Preview
		}
		if rel == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(a.chat.dir, rel))
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
							Material(glass.Glass{Tint: t.Surface.Alpha(0.7)}).
							Border(1, t.Border.Alpha(0.45))
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

// chatRows collapses all tool calls of one turn into a single row at the
// first tool's place, so a turn does not grow a chip per call.
