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
					a.startAnalysis(a.chat)
					return
				}
				a.confirmReanalyze()
			})
			field := ui.Row(c).Grow(1).MinHeight(34).Padding(1, 12).Radius(17).
				AlignItems(ui.Center).Material(glass.Glass{Tint: t.Text.Alpha(0.05)}).
				Label("输入栏")
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
					// A busy conversation still accepts the message: it is
					// shown now and its turn waits for the slot.
					a.sendChatOn(a.chat, k.Text())
					k.SetText("")
					return true
				})
			})
			if a.chat.busy {
				glassAction(c, iconStop, "停止", true, ui.Color{}, func() {
					a.stopAgent(a.chat)
				})
			}
			canSend := strings.TrimSpace(a.chat.input) != ""
			glassAction(c, iconSend, "发送", canSend, t.Accent, func() {
				a.sendChatOn(a.chat, a.chat.input)
			})
		})
	})
}

// sendChat sends on the selected day, which is what the composer's keys and
// buttons do.
func (a *app) sendChat(text string) {
	a.sendChatOn(a.chat, text)
}

// sendChatOn accepts a message for a day even while the one turn slot is
// busy. The message appears at once; its prompt waits in a.pending and runs
// as soon as the current turn ends.
func (a *app) sendChatOn(s *chatState, text string) {
	text = strings.TrimSpace(text)
	if text == "" || s == nil {
		return
	}
	if !a.cfg.configured() {
		a.openSettings()
		return
	}
	images := 0
	if !s.sheetsSent && !a.hasPendingTurn(s) {
		images = len(sheetFiles(s.dir, s.manifest))
	}
	a.addMessage(s, &chatMessage{Role: "user", Text: text, Images: images, Done: true, TS: time.Now()})
	s.input = ""
	a.pending = append(a.pending, pendingTurn{state: s, text: text, images: images})
	a.pumpConversation()
}

// hasPendingTurn reports whether a day already has a message waiting.
func (a *app) hasPendingTurn(s *chatState) bool {
	for _, pt := range a.pending {
		if pt.state == s {
			return true
		}
	}
	return false
}

// startQueuedTurn sends a message that waited for the slot. The caller has
// already given the slot to the message's day.
func (a *app) startQueuedTurn(pt pendingTurn) {
	s := pt.state
	if !a.claimTurn(s, false) {
		return
	}
	var images []agent.Image
	prompt := pt.text
	if pt.images > 0 {
		images = a.sheetImagesFor(s)
		if len(images) > 0 && s.manifest != nil {
			prompt = fmt.Sprintf("（附件是今天的照片合集，编号 #1–#%d。）\n\n%s", len(s.manifest.Photos), pt.text)
		}
	}
	a.startPromptTurn(s, prompt, images)
}

// startGreetingTurn sends the opening line the analysis generated, with the
// day's sheets as its first message. The analysis turn already holds the
// slot.
func (a *app) startGreetingTurn(s *chatState, greeting string) {
	images := a.sheetImagesFor(s)
	prompt := greeting
	if len(images) > 0 && s.manifest != nil {
		prompt = fmt.Sprintf("（附件是今天的照片合集，编号 #1–#%d。）\n\n%s", len(s.manifest.Photos), greeting)
	}
	a.addMessage(s, &chatMessage{Role: "user", Text: greeting, Images: len(images), Done: true, TS: time.Now()})
	a.startPromptTurn(s, prompt, images)
}

// startPromptTurn starts the day's agent if needed and sends one prompt. The
// caller already holds the turn slot.
func (a *app) startPromptTurn(s *chatState, prompt string, images []agent.Image) {
	if err := a.startAgent(s); err != nil {
		s.err = err.Error()
		a.finishStreaming(s)
		a.settleTurn(s)
		return
	}
	a.sendPrompt(s, prompt, images)
}

// sendPrompt writes one prompt to the day's running agent. The prompt's
// result arrives as events, and settling the turn releases the slot.
func (a *app) sendPrompt(s *chatState, prompt string, images []agent.Image) {
	c, ctx := s.client, s.ctx
	if c == nil {
		s.err = "Pi 没有启动"
		a.settleTurn(s)
		return
	}
	s.input = ""
	s.busy = true
	s.err = ""
	go func() {
		err := c.Prompt(ctx, prompt, images)
		if err != nil {
			a.onUI(func() {
				if s.client != c {
					return
				}
				s.err = err.Error()
				s.busy = false
				a.finishStreaming(s)
				a.settleTurn(s)
			})
			return
		}
		a.onUI(func() {
			if s.client == c {
				s.sheetsSent = true
			}
		})
	}()
}

// sheetImagesFor returns a day's contact sheets for the Pi prompt.
func (a *app) sheetImagesFor(s *chatState) []agent.Image {
	if s == nil || s.manifest == nil || s.dir == "" {
		return nil
	}
	out := make([]agent.Image, 0, len(s.manifest.Sheets))
	for _, sh := range s.manifest.Sheets {
		data, err := os.ReadFile(filepath.Join(s.dir, sh.File))
		if err != nil {
			continue
		}
		out = append(out, agent.NewImage(data, "image/jpeg"))
	}
	return out
}
