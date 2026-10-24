package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"stilday/agent"
)

func (a *app) startAgent() error {
	if a.chat.client != nil {
		return nil
	}
	if a.chat.dir == "" {
		return errors.New("还没有工作目录")
	}
	target, err := a.piTarget()
	if err != nil {
		return err
	}
	ext, err := installExtension()
	if err != nil {
		return err
	}
	sys := filepath.Join(a.chat.dir, "system.md")
	if err := os.WriteFile(sys, []byte(a.systemPrompt()), 0o644); err != nil {
		return err
	}
	env := append(append([]string{}, target.env...),
		"STILDAY_DAY_DIR="+a.chat.dir,
		"STILDAY_DAY_LOG_SCHEMA="+dayLogSchemaJSON(),
	)
	ctx, cancel := context.WithCancel(context.Background())
	c, err := agent.Start(ctx, agent.Options{
		Bin:          target.bin,
		Dir:          a.chat.dir,
		Env:          env,
		SessionID:    "stilday-" + a.chat.day,
		SessionDir:   filepath.Join(a.chat.dir, ".pi"),
		Extension:    ext,
		SystemPrompt: sys,
		Provider:     target.spec.ID,
		Model:        target.model,
		Tools:        []string{"day_overview", "read", "stilday_save_day"},
	})
	if err != nil {
		cancel()
		return err
	}
	a.chat.ctx, a.chat.cancel, a.chat.client = ctx, cancel, c
	a.chat.piErr = ""
	go a.consumeAgent(c)
	return nil
}

func (a *app) consumeAgent(c *agent.Client) {
	for {
		select {
		case ev := <-c.Events():
			if a.win == nil {
				if a.chat.client == c {
					a.handleAgentEvent(ev)
				}
				continue
			}
			a.win.Update(func() {
				if a.chat.client != c {
					return
				}
				a.handleAgentEvent(ev)
			})
		case <-c.Done():
			err := c.Err()
			stderr := strings.TrimSpace(c.Stderr())
			if a.win != nil {
				a.win.Update(func() {
					if a.chat.client != c {
						return
					}
					a.chat.client = nil
					a.chat.busy = false
					a.finishStreaming()
					if err != nil {
						msg := "Pi 退出：" + err.Error()
						if stderr != "" {
							msg += "\n" + stderr
						}
						a.chat.err = msg
					}
				})
			}
			return
		}
	}
}

func (a *app) handleAgentEvent(ev agent.Event) {
	switch ev.Type {
	case "agent_start":
		a.chat.busy = true
	case "message_start":
		if ev.Message.Role == "assistant" {
			a.finishStreaming()
			m := &chatMessage{Role: "assistant", TS: time.Now(), ID: a.chat.nextID + 1}
			a.chat.nextID++
			a.chat.messages = append(a.chat.messages, m)
			a.chat.streaming = m
		}
	case "message_update":
		if ev.Update.Type == "text_delta" && a.chat.streaming != nil {
			a.chat.streaming.Text += ev.Update.Delta
		}
	case "message_end":
		if ev.Message.Role == "assistant" {
			if a.chat.streaming != nil {
				a.chat.streaming.Text = ev.Message.Text()
				a.finishStreaming()
			}
			if ev.Message.ErrorMessage != "" {
				a.chat.err = ev.Message.ErrorMessage
			}
		}
	case "tool_execution_start":
		m := &chatMessage{
			Role: "tool", Tool: ev.ToolName, CallID: ev.ToolID,
			ToolStatus: "running", TS: time.Now(),
		}
		a.chat.nextID++
		m.ID = a.chat.nextID
		a.chat.messages = append(a.chat.messages, m)
		a.chat.byCall[ev.ToolID] = m
	case "tool_execution_end":
		m := a.chat.byCall[ev.ToolID]
		if m == nil {
			return
		}
		m.Done = true
		m.ToolStatus = "done"
		if ev.IsError {
			m.ToolStatus = "failed"
		}
		m.ToolDetail = toolDetail(ev)
		if ev.ToolName == "stilday_save_day" && !ev.IsError {
			if n := a.saveDayEntries(ev.Result.Details); n > 0 {
				m.ToolDetail = fmt.Sprintf("保存 %d 条", n)
			}
		}
		a.persistChat(m)
	case "agent_settled":
		a.chat.busy = false
		a.finishStreaming()
		a.finishAnalysisTurn()
	case "auto_retry_start":
		a.chat.progress = "模型重试中…"
	case "auto_retry_end":
		if !ev.Aborted {
			a.chat.progress = ""
		}
	case "extension_error":
		a.chat.err = "扩展：" + string(ev.Raw)
	}
}

func (a *app) finishStreaming() {
	m := a.chat.streaming
	if m == nil {
		return
	}
	a.chat.streaming = nil
	if strings.TrimSpace(m.Text) == "" {
		// A turn that only called tools has no text bubble.
		a.removeMessage(m)
		return
	}
	m.Done = true
	a.persistChat(m)
}

func (a *app) removeMessage(m *chatMessage) {
	for i, x := range a.chat.messages {
		if x == m {
			a.chat.messages = append(a.chat.messages[:i], a.chat.messages[i+1:]...)
			return
		}
	}
}

func (a *app) stopAgent() {
	if a.chat.client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		_ = a.chat.client.Abort(ctx)
	}()
}

func (a *app) closeAgent() {
	if a.chat.cancel != nil {
		a.chat.cancel()
	}
	if a.chat.client != nil {
		_ = a.chat.client.Close()
	}
	a.chat.client = nil
	a.chat.ctx = nil
	a.chat.cancel = nil
}
