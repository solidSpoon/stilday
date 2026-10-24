package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/loading"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// chatRows collapses all tool calls of one turn into a single row at the
// first tool's place, so a turn does not grow a chip per call.
func (a *app) chatRows() []chatRow {
	msgs := a.chat.messages
	out := make([]chatRow, 0, len(msgs))
	for i := 0; i < len(msgs); {
		if msgs[i].Role == "user" {
			out = append(out, chatRow{key: msgs[i].ID, msg: msgs[i]})
			i++
			continue
		}
		j := i
		for j < len(msgs) && msgs[j].Role != "user" {
			j++
		}
		firstTool := -1
		var tools []*chatMessage
		for k := i; k < j; k++ {
			if msgs[k].Role == "tool" {
				if firstTool < 0 {
					firstTool = k
				}
				tools = append(tools, msgs[k])
			}
		}
		for k := i; k < j; k++ {
			if msgs[k].Role == "system" {
				continue
			}
			if msgs[k].Role == "tool" {
				if k == firstTool {
					out = append(out, chatRow{key: fmt.Sprintf("tools-%d", tools[0].ID), tools: tools})
				}
				continue
			}
			out = append(out, chatRow{key: msgs[k].ID, msg: msgs[k]})
		}
		i = j
	}
	if a.chat.greetingPending {
		out = append(out, chatRow{key: "greeting-pending", pending: true})
	}
	return out
}

func (a *app) chatRow(c *ui.Context, row chatRow) {
	if row.pending {
		a.greetingPendingRow(c)
		return
	}
	if row.tools != nil {
		a.toolGroup(c, row.tools)
		return
	}
	a.chatMessage(c, row.msg)
}

func (a *app) greetingPendingRow(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Gap(6).AlignItems(ui.End).Children(func() {
		for i, sheet := range a.chat.sheets {
			a.chatSheet(c, i, sheet)
		}
		ui.Row(c).Gap(4).AlignItems(ui.Center).Padding(11, 14).Radius(16).
			Background(t.Background).Children(func() {
			phase := int(a.now.UnixMilli()/220) % 3
			for i := 0; i < 3; i++ {
				alpha := float32(0.35)
				if i == phase {
					alpha = 1
				}
				ui.Box(c).Size(5, 5).Radius(2.5).Background(t.TextMuted.Alpha(alpha))
			}
			c.After(150 * time.Millisecond)
		})
	})
}

// chatSheet is one contact sheet in the conversation: a click opens the
// preview layer at that sheet, and the pointer lifts it a little.
func (a *app) chatSheet(c *ui.Context, i int, b *ui.Bitmap) {
	if b == nil {
		return
	}
	t := c.Theme()
	img := ui.Image(c, b).Width(280).Radius(12).Fit(ui.Contain).
		Border(1, t.Border.Alpha(0.3)).Cursor(ui.CursorPointer).
		Label("今日合集").Description(fmt.Sprintf("合集 %d", i+1))
	pose := ui.Pose{}
	if img.Hovered() {
		pose = ui.Pose{Scale: 1.015}
	}
	img.AnimatePose(pose, ui.Snappy)
	if img.Clicked() {
		a.openPreview(a.sheetPreviews(), i, false)
	}
}

func (a *app) chatMessage(c *ui.Context, m *chatMessage) {
	t := c.Theme()
	switch m.Role {
	case "user":
		// The owner speaks in the accent color on the right, as Messages
		// draws what one sends. The first analysis message carries the
		// day's contact sheet, so the resource pane no longer has to show it.
		ui.Column(c).Gap(6).AlignItems(ui.End).Children(func() {
			if m.Images > 0 {
				for i, sheet := range a.chat.sheets {
					a.chatSheet(c, i, sheet)
				}
			}
			ui.Text(c, m.Text).Selectable().Padding(10, 14).Radius(16).
				Background(t.Accent).TextColor(t.AccentText).MaxWidth(560)
		})
	case "tool":
		a.toolGroup(c, []*chatMessage{m})
	case "system":
		return
	default:
		ui.Column(c).MaxWidth(760).Gap(6).Children(func() {
			if m.Text != "" {
				if m.Done {
					markdown.View(c, m.Text, &a.chat.md)
				} else {
					markdown.Stream(c, m.Text, &a.chat.md)
				}
			} else if !m.Done {
				// The reply has not started: three dots, as Messages
				// draws a message being written.
				ui.Row(c).Padding(10, 14).Radius(16).Background(t.Background).
					AlignSelf(ui.Start).Label("思考中").Children(func() {
					loading.BouncingDots(c, loading.Options{Size: 13, Color: t.TextMuted})
				})
			}
		})
	}
}

func (a *app) toolGroup(c *ui.Context, tools []*chatMessage) {
	t := c.Theme()
	color := t.Success
	running, failed := false, false
	counts := map[string]int{}
	var order []string
	for _, m := range tools {
		if m.ToolStatus == "running" {
			running = true
		}
		if m.ToolStatus == "failed" {
			failed = true
		}
		label := toolLabel(m)
		if label == "" {
			continue
		}
		if counts[label] == 0 {
			order = append(order, label)
		}
		counts[label]++
	}
	var summary []string
	for _, label := range order {
		if n := counts[label]; n > 1 {
			summary = append(summary, fmt.Sprintf("%s ×%d", label, n))
		} else {
			summary = append(summary, label)
		}
	}
	switch {
	case failed:
		color = t.Danger
	case running:
		color = t.Accent
	}
	var detail []string
	for _, m := range tools {
		d := toolLabel(m) + " · " + toolStatusLabel(m)
		if m.ToolDetail != "" {
			d += " · " + m.ToolDetail
		}
		detail = append(detail, d)
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(6, 12).Radius(8).AlignSelf(ui.Start).
		Background(t.Background).Tooltip(strings.Join(detail, "\n")).Children(func() {
		if running {
			ui.Spinner(c).Size(11, 11)
		} else {
			dot(c, color, 6)
		}
		ui.Text(c, "工具调用").FontSize(11).TextColor(t.TextMuted)
		ui.Text(c, strings.Join(summary, " · ")).FontSize(11.5).TextColor(t.Text).
			SingleLine().Grow(1).MinWidth(0)
	})
}

func toolLabel(m *chatMessage) string {
	switch m.Tool {
	case "day_overview":
		return "查看今日合集"
	case "photo_detail":
		return "查看照片细节"
	case "stilday_save_day":
		return "保存当天日志"
	default:
		if m.Tool == "" {
			return "工具"
		}
		return m.Tool
	}
}

func toolStatusLabel(m *chatMessage) string {
	switch m.ToolStatus {
	case "done":
		return "完成"
	case "failed":
		return "失败"
	default:
		return "运行中"
	}
}
