package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// materialsPanel is the day's raw input: the contact sheets, every photo's
// time, and the manifest.
func (a *app) materialsPanel(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).FillHeight().Background(t.Surface).Children(func() {
		m := a.chat.manifest

		if a.chat.preparing {
			ui.Row(c).Padding(12, 16).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(11, 11)
				ui.Text(c, a.chat.progress).FontSize(11).TextColor(t.TextMuted).
					ContentTransition(ui.ContentTransition{})
			})
		}

		if m == nil || len(m.Photos) == 0 {
			hint := "从照片图库按这一天的时段取。"
			if a.chat.preparing {
				hint = "照片整理好后会出现在这里。"
			}
			emptyPane(c, iconPhotos, "还没有照片", hint)
			return
		}

		ui.Scroll(c).Grow(1).Padding(16, 16, 20).Gap(12).Children(func() {
			if m.PhotoNote != "" {
				ui.Text(c, m.PhotoNote).FontSize(10.5).TextColor(t.Warning).LineHeight(1.5)
			}

			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				sectionLabel(c, "大图")
				ui.Spacer(c)
				ui.Textf(c, "%d", len(m.Photos)).FontSize(10.5).TextColor(t.TextMuted).
					ContentTransition(ui.ContentTransition{Numeric: true})
			})
			a.photoGrid(c, m)

			manifest := filepath.Join(a.chat.dir, "manifest.json")
			row := ui.Row(c).Gap(6).AlignItems(ui.Center).Padding(4, 0).Cursor(ui.CursorPointer)
			row.Children(func() {
				ui.Text(c, "manifest.json").Font("monospace").FontSize(10).TextColor(t.TextMuted)
				ui.Spacer(c)
			})
			if row.Hovered() {
				row.Background(t.SurfaceHover)
			}
			if row.Clicked() {
				_ = mygo.Shell.OpenPath(manifest)
			}
		})
	})
}

// photoView is the materials panel's selection, and the preview over the
// window: which photo is chosen, whether the preview shows, and the
// previews decoded for it, the chosen one and its neighbours.
type photoView struct {
	sel     int // index into the day's manifest, -1 for none
	open    bool
	reveal  bool
	refocus bool
	focus   ui.Handle

	// What the preview shows: the day's photos from the grid, or the
	// contact sheets a message carries.
	items      []previewImage
	index      int
	fromPhotos bool

	cache map[string]*ui.Bitmap
	load  map[string]bool
	bad   map[string]bool
}

// photoGrid is the day's photos: click one to choose it, arrows to move,
// space to preview it, as a Finder window of pictures.
func (a *app) photoGrid(c *ui.Context, m *manifest) {
	n := len(m.Photos)
	a.clampPhotos(n)
	cols := 2
	if n == 1 {
		cols = 1
	}
	grid := ui.Grid(c).Columns(cols).Gap(10).Label("大图").
		Focusable().FocusRing(false).Bind(&a.photos.focus)
	if a.photos.refocus {
		// The preview has closed: the keys go back to the photos.
		a.photos.refocus = false
		a.photos.focus.Focus(c)
	}
	grid.HandleInput(func(ev ui.InputEvent) bool { return a.photoKey(ev, n, cols) })
	thumbs := 0
	for _, b := range a.chat.thumbs {
		if b != nil {
			thumbs++
		}
	}
	grid.Children(func() {
		for i := range m.Photos {
			a.photoCell(c, m, i)
		}
	})
	if thumbs == 0 {
		// An old manifest without thumbnails: the numbered tiles still
		// preview, and 整理 gives them pictures.
		ui.Text(c, "整理一次后，这里会显示大图缩略图。").
			FontSize(9.5).TextColor(c.Theme().TextMuted)
	}
}

// photoCell is one photo of the grid: its thumbnail, the ring that marks
// the chosen one, and its number and time.
func (a *app) photoCell(c *ui.Context, m *manifest, i int) {
	t := c.Theme()
	p := m.Photos[i]
	chosen := a.photos.sel == i
	var b *ui.Bitmap
	if i < len(a.chat.thumbs) {
		b = a.chat.thumbs[i]
	}
	name := fmt.Sprintf("大图 %d", p.Index)
	detail := clockText(p.CapturedAt)
	if p.Sheet > 0 {
		detail += fmt.Sprintf("，合集 %d 格 %d", p.Sheet, p.Cell)
	}

	ui.Column(c).Gap(5).Children(func() {
		ring := ui.Transparent
		if chosen {
			ring = t.Accent
		}
		bg := t.Background
		if b == nil {
			bg = t.SurfaceHover
		}
		// The frame keeps its place, its ring and its corners; only the
		// picture inside it moves, so the tile reads as one piece.
		frame := ui.Box(c).AspectRatio(1).Radius(8).Clip().Background(bg).
			Border(2, ring).Cursor(ui.CursorPointer).
			Label(name).Description(detail)
		frame.Children(func() {
			if b == nil {
				// An old manifest with no thumbnails: the number takes the
				// place of the picture, and previews open as usual.
				ui.Column(c).Fill().Center().Children(func() {
					ui.Textf(c, "#%03d", p.Index).Font("monospace").FontSize(10).
						TextColor(t.TextMuted)
				})
				return
			}
			img := ui.Image(c, b).Fill().Fit(ui.Contain)
			pose := ui.Pose{}
			if frame.Hovered() {
				pose = ui.Pose{Scale: 1.04}
			}
			img.AnimatePose(pose, ui.Snappy)
		})
		if chosen && a.photos.reveal {
			// A choice made from the keyboard, or one the arrows of the
			// preview moved: bring it into sight once.
			frame.ScrollIntoView()
			a.photos.reveal = false
		}
		if frame.Clicked() {
			a.selectPhoto(i)
			a.photos.focus.Focus(c)
		}
		if frame.DoubleClicked() {
			a.selectPhoto(i)
			a.openPhoto()
		}
		color := t.TextMuted
		if chosen {
			color = t.Text
		}
		ui.Textf(c, "#%03d %s", p.Index, clockText(p.CapturedAt)).
			Font("monospace").FontSize(10.5).TextColor(color).SingleLine()
	})
}

// photoKey takes the keys of the photo grid while it has the focus: the
// arrows choose a photo, Home and End the ends, and space previews it.
func (a *app) photoKey(ev ui.InputEvent, n, cols int) bool {
	if ev.Kind != ui.InputKeyDown || ev.Mods != 0 {
		return false
	}
	switch ev.Key {
	case ui.KeyLeft:
		a.stepPhoto(-1, n)
	case ui.KeyRight:
		a.stepPhoto(1, n)
	case ui.KeyUp:
		a.stepPhoto(-cols, n)
	case ui.KeyDown:
		a.stepPhoto(cols, n)
	case ui.KeyHome:
		a.selectPhoto(0)
	case ui.KeyEnd:
		a.selectPhoto(n - 1)
	case ui.KeySpace:
		a.openPhoto()
		return true
	default:
		return false
	}
	return true
}

// stepPhoto moves the choice by delta, stopping at the ends.
func (a *app) stepPhoto(delta, n int) {
	if n == 0 {
		return
	}
	if a.photos.sel < 0 {
		a.selectPhoto(0)
		return
	}
	a.selectPhoto(min(max(a.photos.sel+delta, 0), n-1))
}

// selectPhoto chooses a photo, decoding its preview when the preview
// layer shows it.
func (a *app) selectPhoto(i int) {
	if a.photos.sel == i {
		return
	}
	a.photos.sel = i
	a.photos.reveal = true
}

// previewImage is one picture the preview layer shows: a file in the
// day's folder, and the line under the count that describes it.
type previewImage struct {
	dir  string
	file string // relative to dir
	sub  string
}

// path is the picture's full path.
func (p previewImage) path() string { return filepath.Join(p.dir, p.file) }

// photoPreviews are the day's photos, as the preview layer shows them.
func (a *app) photoPreviews() []previewImage {
	m := a.chat.manifest
	if m == nil {
		return nil
	}
	out := make([]previewImage, 0, len(m.Photos))
	for _, p := range m.Photos {
		out = append(out, previewImage{dir: a.chat.dir, file: p.Preview, sub: clockText(p.CapturedAt)})
	}
	return out
}

// sheetPreviews are the contact sheets a message carries, with the span
// of time each one covers.
func (a *app) sheetPreviews() []previewImage {
	m := a.chat.manifest
	if m == nil {
		return nil
	}
	out := make([]previewImage, 0, len(m.Sheets))
	for _, s := range m.Sheets {
		sub := fmt.Sprintf("合集 %d", s.Index)
		from, to := clockText(s.From), clockText(s.To)
		if from != "" {
			sub += " · " + from
			if to != "" && to != from {
				sub += "–" + to
			}
		}
		out = append(out, previewImage{dir: a.chat.dir, file: s.File, sub: sub})
	}
	return out
}

// openPhoto shows the chosen photo over the window.
func (a *app) openPhoto() {
	if a.photos.sel < 0 {
		return
	}
	a.openPreview(a.photoPreviews(), a.photos.sel, true)
}

// openPreview shows items over the window, starting at index. One opened
// from the grid keeps the grid's choice in step with it.
func (a *app) openPreview(items []previewImage, index int, fromPhotos bool) {
	if len(items) == 0 || index < 0 || index >= len(items) {
		return
	}
	a.photos.items = items
	a.photos.index = index
	a.photos.fromPhotos = fromPhotos
	a.photos.open = true
	a.loadAround()
}

// stepPreview moves the preview by delta, stopping at the ends, and the
// grid's ring with it when the preview came from the grid.
func (a *app) stepPreview(delta int) {
	n := len(a.photos.items)
	if n == 0 {
		return
	}
	i := min(max(a.photos.index+delta, 0), n-1)
	if i == a.photos.index {
		return
	}
	a.photos.index = i
	if a.photos.fromPhotos {
		a.photos.sel = i
		a.photos.reveal = true
	}
	a.loadAround()
}

// clampPhotos keeps the choice inside the day's photos.
func (a *app) clampPhotos(n int) {
	if a.photos.sel >= n {
		a.photos.sel = n - 1
	}
	if n == 0 {
		a.photos.sel = -1
	}
	if a.photos.sel < -1 {
		a.photos.sel = -1
	}
}

// resetPhotos drops the day's choice and the previews decoded for it.
func (a *app) resetPhotos() {
	a.photos.sel = -1
	a.photos.open = false
	a.photos.reveal = false
	a.photos.refocus = false
	a.photos.items = nil
	a.photos.index = 0
	a.photos.fromPhotos = false
	a.photos.cache = map[string]*ui.Bitmap{}
	a.photos.load = map[string]bool{}
	a.photos.bad = map[string]bool{}
}

// loadAround decodes the shown picture and its neighbours, so that the
// arrows find the next one ready, and forgets the ones further away.
func (a *app) loadAround() {
	for d := -1; d <= 1; d++ {
		a.loadPreview(a.photos.index + d)
	}
	a.prunePreviews()
}

// loadPreview decodes one picture off the UI thread, unless it is decoded
// already, being decoded, or known to be missing.
func (a *app) loadPreview(i int) {
	if i < 0 || i >= len(a.photos.items) {
		return
	}
	it := a.photos.items[i]
	key := it.path()
	if it.file == "" || a.photos.cache[key] != nil || a.photos.load[key] || a.photos.bad[key] {
		return
	}
	a.photos.load[key] = true
	if a.win == nil {
		// No window, as in a test: decode here, so the frame that opens
		// the preview has its picture.
		data, err := os.ReadFile(key)
		a.finishPreview(key, data, err)
		return
	}
	go func() {
		data, err := os.ReadFile(key)
		a.win.Update(func() { a.finishPreview(key, data, err) })
	}()
}

// finishPreview installs a decoded picture, or remembers that it could
// not be read.
func (a *app) finishPreview(key string, data []byte, err error) {
	delete(a.photos.load, key)
	if err == nil {
		var b *ui.Bitmap
		if b, err = ui.DecodeBitmap(data); err == nil {
			a.photos.cache[key] = b
			return
		}
	}
	a.photos.bad[key] = true
}

// prunePreviews keeps the pictures around the shown one, and drops the
// others, which includes the days before.
func (a *app) prunePreviews() {
	keep := map[string]*ui.Bitmap{}
	for j := a.photos.index - 2; j <= a.photos.index+2; j++ {
		if j < 0 || j >= len(a.photos.items) {
			continue
		}
		if key := a.photos.items[j].path(); a.photos.cache[key] != nil {
			keep[key] = a.photos.cache[key]
		}
	}
	a.photos.cache = keep
}

// photoPreviewLayer is the preview over the window, as Quick Look opens
// one: the chosen picture on the dimmed window, the arrows switching it
// and space closing it.
func (a *app) photoPreviewLayer(c *ui.Context) {
	if !a.photos.open {
		return
	}
	if len(a.photos.items) == 0 {
		a.photos.open = false
		return
	}
	a.photos.index = min(max(a.photos.index, 0), len(a.photos.items)-1)
	it := a.photos.items[a.photos.index]
	n := len(a.photos.items)
	t := c.Theme()
	ui.DialogBase(c, &a.photos.open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.62))
		panel.WidthPercent(90).HeightPercent(88).Radius(16).Clip().
			Background(t.Background).Border(1, t.Border.Alpha(0.4)).
			Focusable().FocusRing(false).AutoFocus().
			Label(fmt.Sprintf("预览 %d / %d", a.photos.index+1, n))
		panel.HandleInput(func(ev ui.InputEvent) bool {
			if ev.Kind != ui.InputKeyDown || ev.Mods != 0 {
				return false
			}
			switch ev.Key {
			case ui.KeyLeft:
				a.stepPreview(-1)
			case ui.KeyRight:
				a.stepPreview(1)
			case ui.KeySpace:
				a.closePreview()
			default:
				return false
			}
			return true
		})
		panel.Children(func() {
			ui.Row(c).Padding(12, 16).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Textf(c, "%d", a.photos.index+1).FontSize(12).Bold().
					ContentTransition(ui.ContentTransition{Numeric: true})
				ui.Textf(c, "/ %d", n).FontSize(12).TextColor(t.TextMuted)
				ui.Text(c, it.sub).FontSize(11).TextColor(t.TextMuted)
				ui.Spacer(c)
				ui.Text(c, "← → 切换 · 空格 关闭").FontSize(11).TextColor(t.TextMuted)
			})
			ui.Box(c).Grow(1).MinHeight(0).Padding(0, 16, 16).Children(func() {
				a.previewPicture(c, it)
			})
		})
		// Escape or a click beside it closed the preview: hand the keys
		// back to the grid, as a dialog gives the focus back.
		if panel.Dismissed() {
			a.closePreview()
		}
	})
}

// closePreview puts the preview away, and gives the keys back to the
// photo grid when the preview came from it.
func (a *app) closePreview() {
	a.photos.open = false
	a.photos.refocus = a.photos.fromPhotos
}

// previewPicture is the picture of the preview, its spinner while it
// decodes, or why it could not.
func (a *app) previewPicture(c *ui.Context, it previewImage) {
	key := it.path()
	b := a.photos.cache[key]
	switch {
	case b != nil:
		ui.Image(c, b).Fit(ui.Contain).Fill().Radius(10).
			ContentTransition(ui.ContentTransition{}).
			Label("预览 " + it.file)
	case a.photos.bad[key] || it.file == "":
		t := c.Theme()
		ui.Column(c).Fill().Center().Gap(8).Children(func() {
			ui.Icon(c, iconPhotos).FontSize(28).TextColor(t.TextMuted.Alpha(0.5))
			ui.Text(c, "无法载入这张预览").FontSize(12).TextColor(t.TextMuted)
		})
	default:
		t := c.Theme()
		ui.Column(c).Fill().Center().Gap(10).Children(func() {
			ui.Spinner(c).Size(16, 16)
			ui.Text(c, "正在载入预览…").FontSize(11).TextColor(t.TextMuted)
		})
	}
}

// outputPanel is the day's timeline: what the model wrote, oldest first.
func (a *app) outputPanel(c *ui.Context) {
	t := c.Theme()
	entries := append([]Entry(nil), a.entries...)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].OccurredAt.Before(entries[j].OccurredAt) })

	ui.Column(c).FillHeight().Background(t.Surface).Children(func() {
		if len(entries) == 0 {
			hint := "分析后按时间出现在这里。"
			switch {
			case a.chat.preparing:
				hint = "正在整理照片，之后就可以分析这一天。"
			case a.chat.busy:
				hint = "正在分析，事件会陆续出现在这里。"
			case a.chat.piErr != "":
				hint = "还没有 Pi；在设置里下载后就能分析。"
			}
			emptyPane(c, iconEvents, "还没有事件", hint)
			return
		}

		ui.Scroll(c).Grow(1).Padding(10, 14, 20).Gap(10).Children(func() {
			for _, e := range entries {
				card := a.compactEventCard(c, e, e.Seq == a.outputFocus, t.Background)
				// An event the model has just written comes in like a
				// message, and the ones already there keep their place.
				card.Transition(ui.ElementTransition{
					Colors:    true,
					Enter:     &ui.Motion{Y: 8},
					Animation: &ui.Snappy,
				})
				if card.Clicked() {
					a.outputFocus = e.Seq
				}
			}
		})
	})
}

// compactEventCard is an event in a side panel or on the global board.
func (a *app) compactEventCard(c *ui.Context, e Entry, focus bool, bg ui.Color) ui.Element {
	t := c.Theme()
	color := kindColor(e.Kind, t)
	card := ui.Row(c).Gap(10).Padding(10, 12).Radius(10).AlignItems(ui.Stretch).
		Border(1, ui.Transparent).Background(bg).Cursor(ui.CursorPointer)
	card.Transition(ui.ElementTransition{Duration: 120 * time.Millisecond, Colors: true})
	card.Children(func() {
		ui.Box(c).Width(3).Radius(2).Background(color)
		ui.Column(c).Gap(4).Grow(1).MinWidth(0).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, e.OccurredAt.Format("15:04")).Font("monospace").FontSize(11).
					TextColor(t.TextMuted)
				ui.Text(c, kindLabel(e.Kind)).FontSize(10).Bold().TextColor(color).
					Padding(1, 6).Radius(4).Background(color.Alpha(0.12))
				if e.Day != "" && e.Day != e.OccurredAt.Format("2006-01-02") {
					ui.Text(c, e.OccurredAt.Format("01-02")).Font("monospace").FontSize(10).
						TextColor(t.TextMuted)
				}
				ui.Spacer(c)
			})
			ui.Text(c, e.Summary).FontSize(12.5).LineHeight(1.5).TextColor(t.Text)
		})
	})
	if focus {
		card.Border(1, t.Accent.Alpha(0.55)).Background(t.Accent.Alpha(0.08))
	} else if card.Hovered() {
		card.Background(t.SurfaceHover)
	}
	return card
}

// startAnalysis asks the model to look at the day and write events.
func (a *app) startAnalysis() {
	if !a.chatIdle() {
		return
	}
	if a.chat.piErr != "" {
		a.openSettings()
		return
	}
	if !a.hasProviderKey() {
		a.openSettings()
		a.settingsErr = "先在设置里保存 API key，再分析这一天。"
		return
	}
	if a.chat.dir == "" {
		a.openDay(a.day)
	}
	a.beginAnalysisSession()
	a.addMessage(&chatMessage{Role: "system", Text: a.systemPrompt(), Done: true, TS: time.Now()})
	a.chat.greetingPending = true
	// Capture the sheets and the Pi target on the UI thread; the greeting
	// worker must not read the app's state.
	sheets := a.sheetData()
	target, _ := a.piTarget()
	go a.prepareGreeting(target, sheets)
}

// beginAnalysisSession clears the previous analysis: its events, its
// conversation, and its Pi session. The day's photos and manifest stay.
func (a *app) beginAnalysisSession() {
	a.restartConversation()
	a.clearDayAnalysis(a.day, a.chat.dir)
	a.entries = nil
	a.outputFocus = 0
	a.seq = 0
	a.boardDirty = true
}

// restartConversation closes the agent and drops the thread, keeping the
// day and the material it was built from.
func (a *app) restartConversation() {
	a.closeAgent()
	a.chat.clearMessages()
	a.chat.busy = false
	a.chat.preparing = false
	a.chat.progress = ""
	a.chat.err = ""
	a.chat.input = ""
	a.chat.sheetsSent = false
	a.chat.autoAnalyze = false
	a.chat.systemOpen = false
}

// clearDayAnalysis removes a day's artifact lines, transcript and Pi
// session from disk. The photos, manifest and collector state stay.
func (a *app) clearDayAnalysis(day time.Time, dir string) {
	if dir != "" {
		_ = os.Remove(filepath.Join(dir, "chat.jsonl"))
		_ = os.RemoveAll(filepath.Join(dir, ".pi"))
	}
	if err := a.store.ClearDay(day); err != nil {
		a.fileErr = err
	}
}

// confirmReanalyze asks before deleting the day's events and conversation.
func (a *app) confirmReanalyze() {
	if a.win == nil {
		a.reanalyze()
		return
	}
	go func() {
		res, err := mygo.Dialog.Message(mygo.MessageOptions{
			Parent:  a.win,
			Type:    mygo.MessageWarning,
			Message: "重新分析 " + a.day.Format("2006-01-02") + "？",
			Detail:  "会删除这一天的 AI 事件和对话，然后重新看照片生成。",
			Buttons: []string{"重新分析", "取消"},
		})
		if err != nil || res.Button != 0 {
			return
		}
		a.win.Update(func() { a.reanalyze() })
	}()
}

func (a *app) reanalyze() {
	// The confirmation dialog can sit open while the app gets busy, so
	// ask the same condition again before deleting the old analysis.
	if !a.canStartWork() {
		return
	}
	a.startAnalysis()
}

// clearAnalysis deletes the day's artifact and conversation, keeping the
// photos and manifest.
func (a *app) clearAnalysis() {
	if !a.cfg.configured() {
		return
	}
	dir := a.cfg.workDayDir(a.day)
	a.resetForDay()
	a.clearDayAnalysis(a.day, dir)
	a.entries = nil
	a.outputFocus = 0
	a.seq = 0
	a.chat.forcePrep = true
	a.chat.autoAnalyze = a.win != nil
	a.boardDirty = true
	a.reloadCalendar()
	a.openDay(a.day)
}

// emptyPane is the quiet placeholder of a day panel: an icon, what is
// missing, and what will put something there. It fills the panel and sits
// in the middle of it, so the materials and events columns read alike.
func emptyPane(c *ui.Context, icon *ui.SVG, title, hint string) {
	t := c.Theme()
	ui.Column(c).Grow(1).Center().Gap(10).Padding(24).Children(func() {
		ui.Icon(c, icon).FontSize(28).TextColor(t.TextMuted.Alpha(0.5))
		ui.Text(c, title).FontSize(14).Bold().TextColor(t.Text)
		ui.Text(c, hint).FontSize(11).TextColor(t.TextMuted).TextAlign(ui.Center).
			MaxWidth(300).LineHeight(1.5)
	})
}
