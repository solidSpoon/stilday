package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"stilday/agent"
)

// sheetDataFor is every contact sheet of a day, the images the one-off
// greeting call sees.
func (a *app) sheetDataFor(s *chatState) [][]byte {
	if s == nil {
		return nil
	}
	return sheetFiles(s.dir, s.manifest)
}

// prepareGreeting runs before the analysis prompt: it asks a one-off Pi in
// a temporary directory for a fresh opening line from the day's screenshot,
// then puts it in that day's conversation and sends it. The day already
// holds the turn slot.
func (a *app) prepareGreeting(s *chatState, target piTarget, sheets [][]byte) {
	greeting := ""
	if len(sheets) > 0 && a.win != nil && target.bin != "" {
		greeting = a.generateGreeting(target, sheets)
	}
	greeting = cleanGreeting(greeting)
	if greeting == "" {
		greeting = "今天也发生了一些小事，想跟你分享一下。"
	}
	if a.win == nil {
		// No frame loop (tests): release the slot; nothing will send.
		s.greetingPending = false
		a.settleTurn(s)
		return
	}
	a.win.Update(func() {
		if a.turn != s || !s.greetingPending {
			return
		}
		s.greetingPending = false
		a.startGreetingTurn(s, greeting)
	})
}

// generateGreeting is a separate, one-shot Pi session with no tools. It
// only writes the short friend-to-friend opening line.
func (a *app) generateGreeting(target piTarget, images [][]byte) string {
	tmp, err := os.MkdirTemp("", "stilday-greeting-*")
	if err != nil {
		return ""
	}
	defer os.RemoveAll(tmp)
	sysPath := filepath.Join(tmp, "system.md")
	sys := "You are the user, sharing the day with a close friend. The attached image is the day's contact sheet. Write one short, warm opening line that makes the friend want to look. Output only that line, in Chinese, without quotes, without technical words, tool names, JSON or explanations."
	if err := os.WriteFile(sysPath, []byte(sys), 0o644); err != nil {
		return ""
	}
	env := append(append([]string{}, target.env...), "STILDAY_GREETING=1")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	c, err := agent.Start(ctx, agent.Options{
		Bin:          target.bin,
		Dir:          tmp,
		Env:          env,
		SessionID:    fmt.Sprintf("stilday-greeting-%d", time.Now().UnixNano()),
		SessionDir:   filepath.Join(tmp, ".pi"),
		SystemPrompt: sysPath,
		Provider:     target.spec.ID,
		Model:        target.model,
		NoTools:      true,
	})
	if err != nil {
		return ""
	}
	defer c.Close()
	prompt := "这些图是我今天全部的照片合集截图。请假装是我本人，在给一个很好的朋友分享今天；写一句自然、简短、有点期待感的开场白，让朋友想继续看。只输出这一句话，不要引号、不要解释、不要提工具、JSON 或分析。"
	imgs := make([]agent.Image, 0, len(images))
	for _, b := range images {
		imgs = append(imgs, agent.NewImage(b, "image/jpeg"))
	}
	if err := c.Prompt(ctx, prompt, imgs); err != nil {
		return ""
	}
	var text string
	for {
		select {
		case ev := <-c.Events():
			if ev.Type == "message_end" && ev.Message.Role == "assistant" {
				text = ev.Message.Text()
			}
			if ev.Type == "agent_settled" {
				return text
			}
		case <-ctx.Done():
			return text
		case <-c.Done():
			return text
		}
	}
}

func cleanGreeting(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "\"'“”‘’")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > 80 {
		s = string([]rune(s)[:80])
	}
	return s
}
