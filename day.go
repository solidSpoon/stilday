package main

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

// The day's columns: the chat keeps a width of its own, which the right
// seam adjusts; the materials and the events share the rest and the
// framework's split drags freely between them. The calendar sidebar has
// its own narrow and wide states.
const (
	defaultChatWidth      = 370
	defaultMaterialsShare = 73
	minChatWidth          = 240
	maxChatWidth          = 480
)

func (a *app) dayPage(c *ui.Context) {
	if !a.cfg.configured() {
		a.openSettings()
		return
	}
	a.openDay(a.day)
	t := c.Theme()
	title, sub := a.dayTitle()
	pageShell(c, title, sub, func() {
		glassAction(c, iconLayout, "重置布局", true, ui.Color{}, a.resetDayLayout)
	}, func() {
		ui.Box(c).Fill().Padding(barHeight, 0, 0, 0).Children(func() {
			ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
				ui.SplitPercent(c, &a.materialsShare, func() {
					a.materialsPanel(c)
				}, func() {
					a.outputPanel(c)
				}).Grow(1).MinWidth(0).FillHeight()
				a.chatDivider(c)
				// The conversation is the thread, white as Messages draws
				// it; the composer is its last row, not a pane floating
				// over the messages.
				ui.Column(c).Width(a.chatWidth).Shrink(0).Background(t.Surface).Label("对话栏").Children(func() {
					ui.Column(c).Grow(1).MinHeight(0).Children(func() {
						a.chatMessages(c)
						a.chatHeader(c)
					})
					a.composer(c)
				})
			})
		})
	})
	a.systemPromptModal(c)
}

// resetDayLayout restores the day's default widths: the chat at 370, the
// materials at 73% of the left, and the wide calendar.
func (a *app) resetDayLayout() {
	a.chatWidth = defaultChatWidth
	a.materialsShare = defaultMaterialsShare
	a.sidebarWide = true
}

// chatDivider draws the seam before the chat: dragging it changes the
// chat's fixed width, while the materials and events keep their share of
// the room that is left.
func (a *app) chatDivider(c *ui.Context) {
	t := c.Theme()
	label := "调整对话宽度"
	// A plain one-DIP line, with a transparent strip over it for the
	// pointer; no focus ring or hover slab around the seam.
	d := ui.Box(c).Width(9).FillHeight().Background(t.Surface).Cursor(ui.CursorResizeEW).
		Label(label).Tooltip(label)
	d.Draw(func(p *ui.Painter, r ui.Rect) {
		color := t.Border
		if d.Hovered() {
			color = t.Accent
		}
		p.Fill(ui.Rect{X: r.X + r.W/2 - 0.5, Y: r.Y, W: 1, H: r.H}, color, 0)
	})
	if dx, _, ok := d.Dragged(); ok && dx != 0 {
		a.chatWidth = min(max(a.chatWidth-dx, minChatWidth), maxChatWidth)
	}
}

// dayTitle names the day in the toolbar: 今天 for today, else the date,
// with the day's folder under it.
func (a *app) dayTitle() (title, subtitle string) {
	title = "今天"
	if !sameDay(a.day, a.now) {
		if a.day.Year() == a.now.Year() {
			title = fmt.Sprintf("%d 月 %d 日", int(a.day.Month()), a.day.Day())
		} else {
			title = fmt.Sprintf("%d 年 %d 月 %d 日", a.day.Year(), int(a.day.Month()), a.day.Day())
		}
	}
	subtitle = dayLabel(a.day)
	if a.chat.dir != "" {
		subtitle += " · " + shortenPath(a.chat.dir)
	}
	return title, subtitle
}
