package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
)

// month is a row of the calendar.
type month struct {
	Year  int
	Month time.Month
}

func (m month) key() string {
	if m.Month == 0 {
		return fmt.Sprintf("y:%04d", m.Year)
	}
	return fmt.Sprintf("%04d-%02d", m.Year, int(m.Month))
}

func (m month) label() string {
	if m.Month == 0 {
		return fmt.Sprintf("%d 年", m.Year)
	}
	return fmt.Sprintf("%d 年 %d 月", m.Year, int(m.Month))
}

// sidebar is the calendar: two fixed destinations above it, the months
// newest first, and settings at the bottom. It is the window's own
// navigation material, so it carries no glass of its own: the system's
// vibrancy, or the theme's gray where a window has none.
func (a *app) sidebar(c *ui.Context, vibrant bool) {
	t := c.Theme()
	side := ui.Column(c).Width(a.sidebarWidth()).FillHeight().
		BorderWidth(0, 1, 0, 0).BorderColor(hairline(t))
	// A light layer over the system material keeps the sidebar's labels
	// readable over any desktop, while still showing the material through.
	if vibrant {
		side.Background(t.Surface.Alpha(0.72))
	} else {
		side.Background(t.Background)
	}
	side.Children(func() {
		// The traffic lights sit at the top left of this strip, and the
		// whole strip drags the window. The page names itself in the
		// toolbar; the strip carries no wordmark.
		bar := c.TitleBar()
		ui.Row(c).Height(max(bar.Height, barHeight)).WidthPercent(100).DragWindow()

		ui.Column(c).Gap(2).Padding(10, 12, 8).Children(func() {
			a.navRow(c, "事件", "事件", iconEvents, a.page == pageBoard, func() {
				a.page = pageBoard
			})
			a.navRow(c, "任务", "任务", iconTasks, a.page == pageCollectors, func() {
				a.openCollectorPage()
			})
		})

		// One row names the year and steps it, with nothing else: the year
		// where Calendar puts its section title, Today in the middle of the
		// glass. The list below drops the year's own header, so the year is
		// written once.
		ui.Row(c).Padding(8, 20, 12).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Textf(c, "%d 年", a.calendarYear()).FontSize(17).Bold().TextColor(t.Text)
			ui.Spacer(c)
			a.yearStepper(c)
		})

		// The rows can change while this list builds them: opening a year
		// or choosing a day rebuilds a.months. Keep this frame's slice, so
		// the count, the keys and the row builder all agree.
		rows := a.months
		a.calendar.Key = func(i int) any { return rows[i].key() }
		a.calendar.Header = func(i int) bool { return rows[i].Month == 0 }
		ui.List(c, &a.calendar, len(rows), func(i int) {
			a.monthRow(c, rows[i])
		}).Grow(1).Padding(0, 12)

		a.settingsBar(c)
		a.sidebarResizer(c)
	})
}

// sidebarWidth is the calendar's width in its narrow or wide state.
func (a *app) sidebarWidth() float32 {
	if a.sidebarWide {
		return wideSidebarWidth
	}
	return narrowSidebarWidth
}

// sidebarResizer is the calendar's right edge: click it, drag it, or use
// the arrows while it has the focus to switch between the two widths.
func (a *app) sidebarResizer(c *ui.Context) {
	t := c.Theme()
	r := ui.Box(c).Absolute().Top(0).Right(-4).Width(9).FillHeight().
		Cursor(ui.CursorResizeEW).Focusable().Role(ui.RoleSplitter).
		Label("调整日历宽度").Tooltip("调整日历宽度")
	r.Draw(func(p *ui.Painter, rect ui.Rect) {
		if r.Hovered() || r.Focused() {
			p.Fill(ui.Rect{X: rect.X + rect.W/2 - 0.5, Y: rect.Y, W: 1, H: rect.H}, t.Accent.Alpha(0.65), 0)
		}
	})
	if r.Focused() {
		if r.Shortcut(0, ui.KeyLeft) {
			a.sidebarWide = false
		}
		if r.Shortcut(0, ui.KeyRight) {
			a.sidebarWide = true
		}
	}
	if dx, _, ok := r.Dragged(); ok && dx != 0 {
		if dx > 1 {
			a.sidebarWide = true
		} else if dx < -1 {
			a.sidebarWide = false
		}
	} else if r.Clicked() {
		a.sidebarWide = !a.sidebarWide
	}
}

// yearStepper steps the calendar a year up or down, with Today in the
// middle: Calendar's period control turned on its side, for a list that
// scrolls vertically. Three pieces of glass, a chevron, Today, a chevron.
func (a *app) yearStepper(c *ui.Context) {
	t := c.Theme()
	today := midnight(a.now)
	ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
		a.yearStep(c, iconChevronUp, "跳到更近的年份", 1)
		pill := ui.Row(c).Height(32).MinWidth(64).Padding(0, 14).Radius(16).
			Material(glass.Glass{}).Center().Cursor(ui.CursorPointer).Label("回到今天")
		pill.Children(func() {
			ui.Text(c, "今天").FontSize(13).Bold().TextColor(t.Text)
		})
		if pill.Clicked() {
			a.selectDay(today)
			a.scrollToMonth(today.Year(), today.Month())
		}
		a.yearStep(c, iconChevronDn, "跳到更早的年份", -1)
	})
}

func (a *app) yearStep(c *ui.Context, icon *ui.SVG, label string, delta int) {
	t := c.Theme()
	year := a.calendarYear()
	enabled := delta > 0 && year < a.now.Year() || delta < 0 && year > calendarStartYear
	b := ui.Row(c).Size(32, 32).Radius(16).Center().
		Material(glass.Glass{Interactive: enabled}).Label(label).Tooltip(label)
	color := t.Text
	if enabled {
		b.Cursor(ui.CursorPointer)
	} else {
		// The capsule stays, the chevron dims, as AppKit draws a disabled
		// toolbar button.
		b.Cursor(ui.CursorDefault)
		color = t.TextMuted.Alpha(0.9)
	}
	b.Children(func() {
		ui.Icon(c, icon).FontSize(13).TextColor(color)
	})
	if enabled && b.Clicked() {
		a.calYear = year
		a.jumpYear(delta)
	}
}

// navRow is one fixed destination above the calendar: a sidebar item, as
// AppKit's source list draws one.
func (a *app) navRow(c *ui.Context, label, accessibility string, icon *ui.SVG, chosen bool, action func()) {
	t := c.Theme()
	row := ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(6, 10).Radius(8).
		Height(32).Cursor(ui.CursorPointer).Label(accessibility)
	// The fill fades under the pointer, as a source list's row does.
	row.Transition(ui.ElementTransition{Duration: 120 * time.Millisecond, Colors: true})
	row.Children(func() {
		color := t.TextMuted
		if chosen {
			color = t.Accent
		}
		ui.Icon(c, icon).FontSize(16).TextColor(color)
		text := ui.Text(c, label).FontSize(13)
		if chosen {
			text = text.Bold()
		}
	})
	switch {
	case chosen:
		row.Background(t.Accent.Alpha(0.12)).TextColor(t.Accent)
	case row.Hovered():
		row.Background(t.SurfaceHover)
	}
	if row.Clicked() {
		action()
	}
}

// backgroundStatus is the one summary beside 设置: a manual turn says so,
// otherwise the number of days waiting. The analysis and collection
// progress live on their queue cards.
func (a *app) backgroundStatus(c *ui.Context) {
	t := c.Theme()
	if s := a.scheduler(); s != nil && s.HasRunning() {
		// The running card carries the progress.
		return
	}
	if a.turn != nil {
		ui.Text(c, "对话中").FontSize(10.5).TextColor(t.TextMuted)
		return
	}
	if s := a.scheduler(); s != nil {
		if n := s.CountPending(); n > 0 {
			ui.Textf(c, "%d 天待分析", n).FontSize(10.5).TextColor(t.TextMuted).
				ContentTransition(ui.ContentTransition{Numeric: true})
		}
	}
}

// settingsBar is the row at the bottom of the sidebar.
func (a *app) settingsBar(c *ui.Context) {
	t := c.Theme()
	row := ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(16, 16).Cursor(ui.CursorPointer).
		Label("打开设置").BorderWidth(1, 0, 0, 0).BorderColor(hairline(t))
	row.Transition(ui.ElementTransition{Duration: 120 * time.Millisecond, Colors: true})
	row.Children(func() {
		color := t.TextMuted
		if a.page == pageSettings {
			color = t.Accent
		}
		ui.Icon(c, iconGear).FontSize(16).TextColor(color)
		ui.Text(c, "设置").FontSize(13)
		ui.Spacer(c)
		a.backgroundStatus(c)
	})
	switch {
	case a.page == pageSettings:
		row.Background(t.SurfaceHover).TextColor(t.Text)
	case row.Hovered():
		row.Background(t.SurfaceHover)
	}
	if row.Clicked() {
		a.openSettings()
	}
}

// selectedMonthLabel names the month of the selected day, so the calendar
// can be scrolled without losing the selection.
func (a *app) selectedMonthLabel() string {
	m := month{a.day.Year(), a.day.Month()}
	return m.label()
}

// hasMonth reports whether the current rows contain a month.
func (a *app) hasMonth(year int, mon time.Month) bool {
	for _, m := range a.months {
		if m.Year == year && m.Month == mon {
			return true
		}
	}
	return false
}

// scrollToMonth expands the year if needed and scrolls to its month row.
func (a *app) scrollToMonth(year int, mon time.Month) {
	a.calYear = year
	for i, m := range a.months {
		if m.Year == year && m.Month == mon {
			a.calendar.ScrollTo(i, ui.Start)
			return
		}
	}
}

// jumpYear scrolls the calendar to the previous or next year's header.
func (a *app) jumpYear(delta int) {
	y := a.calYear + delta
	if y < calendarStartYear || y > a.now.Year() {
		return
	}
	a.calYear = y
	a.scrollToYear(y)
}

// calendarYear is the year at the top of the calendar, so the header above
// names what the list shows as it scrolls, not only the year last jumped to.
func (a *app) calendarYear() int {
	first, _ := a.calendar.Visible()
	if first >= 0 && first < len(a.months) {
		return a.months[first].Year
	}
	return a.calYear
}

// scrollToYear scrolls the calendar to the year's header, or to its first
// month when the current year has no header of its own.
func (a *app) scrollToYear(year int) {
	for i, m := range a.months {
		if m.Year == year && m.Month == 0 {
			a.calendar.ScrollTo(i, ui.Start)
			return
		}
	}
	// The current year has no header of its own; its first month is the
	// top of its section.
	for i, m := range a.months {
		if m.Year == year && m.Month != 0 {
			a.calendar.ScrollTo(i, ui.Start)
			return
		}
	}
}

// monthRow draws one year or one month of the calendar.
func (a *app) monthRow(c *ui.Context, m month) {
	if m.Month == 0 {
		a.yearRow(c, m.Year)
		return
	}
	t := c.Theme()
	cellW, cellH := float32(34), float32(34)
	if a.sidebarWide {
		cellW, cellH = 40, 40
	}
	const gap = 4
	ui.Column(c).Padding(10, 14, 16).Gap(6).Children(func() {
		ui.Row(c).Padding(4, 2, 4).Children(func() {
			// The year is written once, in the header above; a month is
			// enough here, with the year's sections for older years.
			ui.Textf(c, "%d 月", int(m.Month)).FontSize(14).Bold().TextColor(t.Text)
			ui.Spacer(c)
			if m.Year == a.now.Year() && m.Month == a.now.Month() {
				ui.Text(c, "本月").FontSize(10.5).TextColor(t.TextMuted)
			}
		})
		ui.Row(c).Gap(gap).Justify(ui.Center).Children(func() {
			for _, w := range [7]string{"一", "二", "三", "四", "五", "六", "日"} {
				ui.Box(c).Width(cellW).Height(18).Center().Children(func() {
					ui.Text(c, w).FontSize(11).TextColor(t.TextMuted)
				})
			}
		})
		first := time.Date(m.Year, m.Month, 1, 0, 0, 0, 0, a.day.Location())
		offset := (int(first.Weekday()) + 6) % 7 // Monday first
		days := time.Date(m.Year, m.Month+1, 0, 0, 0, 0, 0, a.day.Location()).Day()
		for week := 0; week < 6; week++ {
			ui.Row(c).Gap(gap).Justify(ui.Center).Children(func() {
				for wd := 0; wd < 7; wd++ {
					pos := week*7 + wd
					n := pos - offset + 1
					if n < 1 || n > days {
						ui.Box(c).Size(cellW, cellH)
						continue
					}
					d := time.Date(m.Year, m.Month, n, 0, 0, 0, 0, a.day.Location())
					a.dayCell(c, d, cellW, cellH)
				}
			})
		}
	})
}

func (a *app) dayCell(c *ui.Context, d time.Time, w, h float32) {
	t := c.Theme()
	key := d.Format("2006-01-02")
	today := sameDay(d, a.now)
	chosen := sameDay(d, a.day)
	future := d.After(midnight(a.now))
	hasData := a.dataDays[key]
	changed := a.calendarChanged[key]
	working := a.aiWorkingDay() == key
	analyzing := working && a.turn != nil && a.turn.analysis

	label := key
	if changed {
		label += "，有采集变化"
	}
	if working {
		if analyzing {
			label += "，AI 正在分析"
		} else {
			label += "，AI 正在回复"
		}
	}
	cell := ui.Box(c).Size(w, h).Radius(10).Center().Label(label)
	cell.Children(func() {
		ui.Column(c).Gap(2).AlignItems(ui.Center).Children(func() {
			ui.Text(c, fmt.Sprint(d.Day())).FontSize(12)
			dot := ui.Box(c).Size(4, 4).Radius(2)
			// A dot means there are collector changes not analyzed yet.
			if changed {
				color := t.Accent
				if chosen {
					color = t.AccentText
				}
				dot.Background(color)
			} else {
				dot.Opacity(0)
			}
		})
	})
	switch {
	case chosen:
		cell.Background(t.Accent).TextColor(t.AccentText)
	case hasData:
		cell.Background(t.SurfacePressed)
	}
	if working {
		// A thin ring marks the day the AI is working on: that day holds
		// the one conversation slot. On the selected (blue) cell the ring
		// is the light text color so it stays visible.
		ring := t.Accent
		if chosen {
			ring = t.AccentText
		}
		cell.Border(1, ring)
	}
	if today && !chosen {
		// Today is a label, not a selected cell: no border and no fill.
		cell.TextColor(t.Accent)
	}
	if !future {
		cell.Cursor(ui.CursorPointer)
		if cell.Hovered() && !chosen {
			cell.Background(t.SurfaceHover)
		}
		if cell.Clicked() {
			a.selectDay(d)
		}
	} else {
		cell.Opacity(0.32)
	}
}

// yearCounts returns how many days of a year have data, and how many of
// them have collector changes the AI has not analyzed.
func (a *app) yearCounts(year int) (count, changed int) {
	prefix := fmt.Sprintf("%d-", year)
	for key := range a.dataDays {
		if strings.HasPrefix(key, prefix) {
			count++
		}
	}
	for key := range a.calendarChanged {
		if strings.HasPrefix(key, prefix) {
			changed++
		}
	}
	return count, changed
}

// yearRow is the collapsible year header. The calendar reaches 2020 even
// when there is no data there; past years start closed so the list stays
// short until they are wanted.
func (a *app) yearRow(c *ui.Context, year int) {
	t := c.Theme()
	count, changed := a.yearCounts(year)
	row := ui.Row(c).Padding(12, 16, 6).Gap(8).AlignItems(ui.Center).
		Label(fmt.Sprintf("%d 年", year))
	row.Children(func() {
		ui.Text(c, fmt.Sprintf("%d", year)).FontSize(12).Bold().TextColor(t.Text)
		ui.Spacer(c)
		if changed > 0 && count > 0 {
			ui.Textf(c, "%d 天 · %d 待分析", count, changed).FontSize(11).TextColor(t.Accent).
				ContentTransition(ui.ContentTransition{Numeric: true})
		} else if changed > 0 {
			ui.Textf(c, "%d 待分析", changed).FontSize(11).TextColor(t.Accent).
				ContentTransition(ui.ContentTransition{Numeric: true})
		} else if count > 0 {
			ui.Textf(c, "%d 天", count).FontSize(11).TextColor(t.TextMuted).
				ContentTransition(ui.ContentTransition{Numeric: true})
		}
	})
}
