package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"

	"stilday/agent"
)

func (a *app) composer(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Padding(10, 16, 16).Gap(6).Children(func() {
		if a.chat.err != "" {
			ui.Row(c).Gap(8).AlignItems(ui.Start).Padding(8, 10).Radius(8).
				Background(t.Danger.Alpha(0.08)).Children(func() {
				ui.Text(c, a.chat.err).FontSize(11).TextColor(t.Danger).Grow(1).Selectable()
				if ui.Button(c, "清除").Clicked() {
					a.chat.err = ""
				}
			})
		}
		// One row of glass, as Messages draws its composer: a round action,
		// the field, and the send button. Re-analyzing the day belongs
		// here, where the conversation that asks for it begins.
		ui.Row(c).Gap(10).AlignItems(ui.End).Children(func() {
			// A day with neither events nor a conversation has nothing to
			// delete: the same control starts its analysis.
			fresh := len(a.entries) == 0 && len(a.chat.messages) == 0
			label := "重新分析这一天"
			if fresh {
				label = "分析这一天"
			}
			canAnalyze := a.canStartWork()
			glassAction(c, iconAI, label, canAnalyze, ui.Color{}, func() {
				if !a.canStartWork() {
					return
				}
				if fresh {
					a.startAnalysis()
					return
				}
				a.confirmReanalyze()
			})
			field := ui.Row(c).Grow(1).MinHeight(34).Padding(1, 12).Radius(17).
				AlignItems(ui.Center).Material(glass.Glass{Tint: t.Text.Alpha(0.05)}).
				Border(1, t.Border.Alpha(0.5)).Label("输入栏")
			field.Children(func() {
				// A text area is 80 DIPs high unless told otherwise; this
				// one is a single row that grows as it is typed in.
				in := ui.TextArea(c, &a.chat.input).Placeholder("问点什么…").Lines(1, 5).
					Grow(1).AutoFocus().MinHeight(0).Padding(0).
					Background(ui.Transparent).Border(0, ui.Transparent)
				in.OnTextKey(func(k ui.TextKey) bool {
					if k.Key != ui.KeyEnter || k.Mods != 0 {
						return false
					}
					if !a.chatIdle() {
						return true
					}
					a.sendChat(k.Text())
					k.SetText("")
					return true
				})
			})
			if a.chat.busy {
				glassAction(c, iconStop, "停止", true, ui.Color{}, func() {
					a.stopAgent()
				})
			} else {
				canSend := strings.TrimSpace(a.chat.input) != "" && a.chatIdle()
				glassAction(c, iconSend, "发送", canSend, t.Accent, func() {
					a.sendChat(a.chat.input)
				})
			}
		})
	})
}

// sendChat starts Pi if needed, attaches the day's sheets to the first
// message, and sends the prompt.

// sendChat starts Pi if needed, attaches the day's sheets to the first
// message, and sends the prompt.
func (a *app) sendChat(text string) {
	text = strings.TrimSpace(text)
	if text == "" || !a.chatIdle() {
		return
	}
	if !a.cfg.configured() {
		a.openSettings()
		return
	}
	if err := a.startAgent(); err != nil {
		a.chat.err = err.Error()
		return
	}
	first := !a.chat.sheetsSent
	var images []agent.Image
	if first {
		images = a.sheetImages()
	}
	prompt := text
	if len(images) > 0 {
		prompt = fmt.Sprintf("（附件是今天的照片合集，编号 #1–#%d。）\n\n%s", len(a.chat.manifest.Photos), text)
	}
	a.addMessage(&chatMessage{Role: "user", Text: text, Images: len(images), Done: true, TS: time.Now()})
	a.chat.input = ""
	a.chat.busy = true
	a.chat.err = ""
	c := a.chat.client
	ctx := a.chat.ctx
	go func() {
		err := c.Prompt(ctx, prompt, images)
		if err != nil {
			if a.win != nil {
				a.win.Update(func() {
					if a.chat.client != c {
						return
					}
					a.chat.err = err.Error()
					a.chat.busy = false
				})
			}
			return
		}
		if a.win != nil {
			a.win.Update(func() {
				if a.chat.client == c {
					a.chat.sheetsSent = true
				}
			})
		}
	}()
}

func (a *app) sheetImages() []agent.Image {
	if a.chat.manifest == nil {
		return nil
	}
	out := make([]agent.Image, 0, len(a.chat.manifest.Sheets))
	for _, s := range a.chat.manifest.Sheets {
		data, err := os.ReadFile(filepath.Join(a.chat.dir, s.File))
		if err != nil {
			continue
		}
		out = append(out, agent.NewImage(data, "image/jpeg"))
	}
	return out
}
