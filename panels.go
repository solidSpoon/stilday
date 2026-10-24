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
				ui.Text(c, a.chat.progress).FontSize(11).TextColor(t.TextMuted)
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
				if len(m.Photos) > 0 {
					ui.Textf(c, "%d", len(m.Photos)).FontSize(10.5).TextColor(t.TextMuted)
				}
			})
			haveThumbs := false
			for _, b := range a.chat.thumbs {
				if b != nil {
					haveThumbs = true
					break
				}
			}
			if haveThumbs {
				cols := 2
				if len(m.Photos) == 1 {
					cols = 1
				}
				ui.Grid(c).Columns(cols).Gap(10).Children(func() {
					for i, p := range m.Photos {
						p, i := p, i
						var b *ui.Bitmap
						if i < len(a.chat.thumbs) {
							b = a.chat.thumbs[i]
						}
						cell := ui.Column(c).Gap(5)
						cell.Children(func() {
							label := fmt.Sprintf("大图 %d，%s", p.Index, clockText(p.CapturedAt))
							if p.Sheet > 0 {
								label += fmt.Sprintf("，合集 %d 格 %d", p.Sheet, p.Cell)
							}
							if b != nil {
								img := ui.Image(c, b).AspectRatio(1).Fit(ui.Contain).Radius(8).
									Background(t.Background).Cursor(ui.CursorPointer).Label(label)
								if img.Clicked() {
									_ = mygo.Shell.OpenPath(filepath.Join(a.chat.dir, p.Preview))
								}
							} else {
								box := ui.Column(c).AspectRatio(1).Center().Radius(8).
									Background(t.SurfaceHover).Cursor(ui.CursorPointer).Label(label)
								box.Children(func() {
									ui.Textf(c, "#%03d", p.Index).Font("monospace").FontSize(10).
										TextColor(t.TextMuted)
								})
								if box.Clicked() {
									_ = mygo.Shell.OpenPath(filepath.Join(a.chat.dir, p.Preview))
								}
							}
							ui.Textf(c, "#%03d %s", p.Index, clockText(p.CapturedAt)).
								Font("monospace").FontSize(10.5).TextColor(t.TextMuted).SingleLine()
						})
					}
				})
			} else {
				// An old manifest with no thumbnails: still let every preview be
				// opened, and say 整理 will add the thumbnails.
				for _, p := range m.Photos {
					p := p
					row := ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(2, 0).
						Cursor(ui.CursorPointer).Label(fmt.Sprintf("大图 %d", p.Index))
					row.Children(func() {
						ui.Textf(c, "#%03d", p.Index).Font("monospace").FontSize(10).
							TextColor(t.TextMuted).Width(34)
						ui.Text(c, clockText(p.CapturedAt)).Font("monospace").FontSize(10).
							TextColor(t.Text)
						ui.Spacer(c)
						if p.Sheet > 0 {
							ui.Textf(c, "合集 %d·%d", p.Sheet, p.Cell).FontSize(9).
								TextColor(t.TextMuted)
						}
					})
					if row.Hovered() {
						row.Background(t.SurfaceHover)
					}
					if row.Clicked() {
						_ = mygo.Shell.OpenPath(filepath.Join(a.chat.dir, p.Preview))
					}
				}
				ui.Text(c, "整理一次后，这里会显示大图缩略图。").FontSize(9.5).TextColor(t.TextMuted)
			}

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
