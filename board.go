package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
)

// boardItem is one row of the global board: a day's heading, or an event.
type boardItem struct {
	Header bool
	Day    string
	Count  int
	Entry  Entry
}

// boardPage is the history of every event, newest first, grouped by the
// day that produced it. It scrolls under the window's toolbar, as a
// document does.
func (a *app) boardPage(c *ui.Context) {
	if !a.cfg.configured() {
		a.openSettings()
		return
	}
	a.loadBoard()
	a.filterBoard()
	days := map[string]bool{}
	for _, e := range a.boardEntries {
		days[e.Day] = true
	}

	subtitle := fmt.Sprintf("%d 个事件 · %d 天", len(a.boardEntries), len(days))
	if a.boardQuery != "" {
		subtitle = fmt.Sprintf("“%s” 的 %d 个事件 · 共 %d 天", a.boardQuery, len(a.boardItems), len(days))
	}
	pageShell(c, "事件", subtitle, func() {
		// The search is a control over content, so it is a pill of glass,
		// as Calendar's search is.
		ui.SearchField(c, &a.boardQuery).Width(220).Height(34).Radius(17).
			Material(glass.Glass{Tint: c.Theme().Text.Alpha(0.07)}).Background(ui.Transparent).
			Border(0, ui.Transparent).Label("搜索所有事件")
	}, func() {
		if len(a.boardItems) == 0 {
			a.emptyBoard(c)
			return
		}
		a.boardList.Key = func(i int) any {
			it := a.boardItems[i]
			if it.Header {
				return "h:" + it.Day
			}
			return it.Entry.Seq
		}
		a.boardList.Header = func(i int) bool { return false }
		a.boardList.Label = func(i int) string {
			it := a.boardItems[i]
			if it.Header {
				return it.Day
			}
			return it.Entry.Summary
		}
		ui.List(c, &a.boardList, len(a.boardItems), func(i int) {
			a.boardRow(c, a.boardItems[i])
		}).Fill().Padding(barHeight+20, 28, 28).Gap(10).
			ScrollbarInsets(barHeight+8, 0, 0)
	})
}

func (a *app) boardRow(c *ui.Context, it boardItem) {
	t := c.Theme()
	if it.Header {
		day, _ := time.ParseInLocation("2006-01-02", it.Day, time.Local)
		ui.Row(c).Justify(ui.Center).Padding(24, 0, 12).Children(func() {
			ui.Row(c).WidthPercent(100).MaxWidth(920).Gap(14).AlignItems(ui.End).Children(func() {
				ui.Text(c, day.Format("02")).FontSize(28).Bold().TextColor(t.Text)
				ui.Column(c).Gap(2).Children(func() {
					ui.Textf(c, "%d 年 %d 月", day.Year(), int(day.Month())).FontSize(11).TextColor(t.TextMuted)
					ui.Textf(c, "%s · %d 个事件", weekday(day), it.Count).FontSize(11).TextColor(t.TextMuted)
				})
				ui.Spacer(c)
			})
		})
		return
	}
	ui.Row(c).Justify(ui.Center).Children(func() {
		ui.Column(c).WidthPercent(100).MaxWidth(880).Children(func() {
			card := a.boardEventCard(c, it.Entry)
			if card.Clicked() {
				a.openBoardEntry(it.Entry)
			}
		})
	})
}

// boardEventCard is the board's timeline card: time, kind, summary, with a
// quiet accent rail. It is wider and calmer than the compact side cards.
func (a *app) boardEventCard(c *ui.Context, e Entry) ui.Element {
	t := c.Theme()
	color := kindColor(e.Kind, t)
	card := ui.Row(c).Gap(14).Padding(16, 18).Radius(12).
		Background(t.Surface).AlignItems(ui.Start).
		Cursor(ui.CursorPointer)
	card.Children(func() {
		ui.Column(c).Width(44).Gap(3).Children(func() {
			ui.Text(c, e.OccurredAt.Format("15:04")).Font("monospace").FontSize(11).TextColor(t.TextMuted)
			if e.Day != "" && e.Day != e.OccurredAt.Format("2006-01-02") {
				ui.Text(c, e.OccurredAt.Format("01-02")).Font("monospace").FontSize(10).TextColor(t.TextMuted)
			}
		})
		ui.Box(c).Width(3).Height(26).Radius(2).Background(color)
		ui.Column(c).Gap(7).Grow(1).MinWidth(0).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, kindLabel(e.Kind)).FontSize(10).Bold().TextColor(color).
					Padding(1, 6).Radius(4).Background(color.Alpha(0.12))
				ui.Spacer(c)
			})
			ui.Text(c, e.Summary).FontSize(13).LineHeight(1.5).TextColor(t.Text)
		})
	})
	if card.Hovered() {
		card.Background(t.SurfaceHover)
	}
	return card
}

func (a *app) emptyBoard(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Center().Gap(12).Padding(100, 28, 28).Children(func() {
		if a.boardQuery != "" {
			ui.Text(c, "没有匹配的事件").FontSize(15).Bold()
			ui.Textf(c, "搜索 “%s”", a.boardQuery).FontSize(11.5).TextColor(t.TextMuted)
			if ui.Button(c, "清除搜索").Clicked() {
				a.boardQuery = ""
			}
			return
		}
		if a.fileErr != nil {
			ui.Text(c, "读取产物失败").FontSize(15).Bold().TextColor(t.Danger)
			ui.Text(c, a.fileErr.Error()).FontSize(11.5).TextColor(t.Danger).Selectable()
			return
		}
		ui.Text(c, "还没有任何事件").FontSize(16).Bold()
		ui.Text(c, "去今天，让 AI 看一遍照片，事件会一条条流进这里。").FontSize(12).TextColor(t.TextMuted)
		if ui.PrimaryButton(c, "去今天").Clicked() {
			today := midnight(time.Now())
			a.selectDay(today)
			a.scrollToMonth(today.Year(), today.Month())
		}
	})
}

// loadBoard reads every artifact, once per change.
func (a *app) loadBoard() {
	if a.boardLoaded && !a.boardDirty {
		return
	}
	entries, err := a.store.LoadAll()
	a.fileErr = err
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Day != entries[j].Day {
			return entries[i].Day > entries[j].Day
		}
		return entries[i].OccurredAt.After(entries[j].OccurredAt)
	})
	for i := range entries {
		entries[i].Seq = int64(i + 1)
	}
	a.boardEntries = entries
	a.boardLoaded = true
	a.boardDirty = false
}

func (a *app) filterBoard() {
	counts := map[string]int{}
	query := strings.ToLower(strings.TrimSpace(a.boardQuery))
	filtered := make([]Entry, 0, len(a.boardEntries))
	for _, e := range a.boardEntries {
		if query != "" {
			hay := strings.ToLower(e.Day + " " + e.Kind + " " + e.Summary)
			if !strings.Contains(hay, query) {
				continue
			}
		}
		filtered = append(filtered, e)
		counts[e.Day]++
	}
	items := make([]boardItem, 0, len(filtered)+len(counts))
	last := ""
	for _, e := range filtered {
		if e.Day != last {
			items = append(items, boardItem{Header: true, Day: e.Day, Count: counts[e.Day]})
			last = e.Day
		}
		items = append(items, boardItem{Entry: e})
	}
	a.boardItems = items
}

// openBoardEntry opens the day that produced an event, focusing it in the
// day's output timeline.
func (a *app) openBoardEntry(e Entry) {
	day, err := time.ParseInLocation("2006-01-02", e.Day, time.Local)
	if err != nil {
		return
	}
	a.selectDay(day)
	a.scrollToMonth(day.Year(), day.Month())
	for _, de := range a.entries {
		if de.OccurredAt.Equal(e.OccurredAt) && de.Kind == e.Kind && de.Summary == e.Summary {
			a.outputFocus = de.Seq
			break
		}
	}
}
