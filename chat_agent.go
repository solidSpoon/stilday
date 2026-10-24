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

// agentStart is the resolved Pi runtime and extension, shared by every day
// so a turn does not resolve them twice.
type agentStart struct {
	target piTarget
	ext    string
}

// prepareAgent resolves what a Pi child needs before any day is touched.
func (a *app) prepareAgent() (agentStart, error) {
	target, err := a.piTarget()
	if err != nil {
		return agentStart{}, err
	}
	ext, err := installExtension()
	if err != nil {
		return agentStart{}, err
	}
	return agentStart{target: target, ext: ext}, nil
}

func (a *app) startAgent(s *chatState) error {
	if s.client != nil {
		return nil
	}
	st, err := a.prepareAgent()
	if err != nil {
		return err
	}
	return a.startAgentWith(s, st)
}

// startAgentWith starts one day's Pi child from a prepared runtime.
func (a *app) startAgentWith(s *chatState, st agentStart) error {
	if s.client != nil {
		return nil
	}
	if s.dir == "" {
		return errors.New("还没有工作目录")
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	sys := filepath.Join(s.dir, "system.md")
	if err := os.WriteFile(sys, []byte(a.systemPromptFor(s.day, s.dir, s.manifest)), 0o644); err != nil {
		return err
	}
	env := append(append([]string{}, st.target.env...),
		"STILDAY_DAY_DIR="+s.dir,
		"STILDAY_DAY_LOG_SCHEMA="+dayLogSchemaJSON(),
	)
	ctx, cancel := context.WithCancel(context.Background())
	c, err := agent.Start(ctx, agent.Options{
		Bin:          st.target.bin,
		Dir:          s.dir,
		Env:          env,
		SessionID:    "stilday-" + s.day,
		SessionDir:   filepath.Join(s.dir, ".pi"),
		Extension:    st.ext,
		SystemPrompt: sys,
		Provider:     st.target.spec.ID,
		Model:        st.target.model,
		Tools:        []string{"day_overview", "read", "stilday_save_day"},
	})
	if err != nil {
		cancel()
		return err
	}
	s.ctx, s.cancel, s.client = ctx, cancel, c
	a.piErr = ""
	go a.consumeAgent(s, c)
	return nil
}

// claimTurn gives the one conversation slot to a day. The caller starts the
// prompt on it.
func (a *app) claimTurn(s *chatState, analysis bool) bool {
	if s == nil || a.turn != nil || a.collecting {
		return false
	}
	a.turn = s
	s.analysis = analysis
	return true
}

// onUI runs fn on the UI thread, or inline when there is no window (tests).
func (a *app) onUI(fn func()) {
	if a.win == nil {
		fn()
		return
	}
	a.win.Update(fn)
}

func (a *app) consumeAgent(s *chatState, c *agent.Client) {
	for {
		select {
		case ev := <-c.Events():
			a.onUI(func() {
				if s.client != c {
					return
				}
				a.handleAgentEvent(s, ev)
			})
		case <-c.Done():
			err := c.Err()
			stderr := strings.TrimSpace(c.Stderr())
			a.onUI(func() {
				if s.client != c {
					return
				}
				s.client = nil
				s.ctx = nil
				s.cancel = nil
				s.busy = false
				a.finishStreaming(s)
				if err != nil && s.err == "" {
					msg := "Pi 退出：" + err.Error()
					if stderr != "" {
						msg += "\n" + stderr
					}
					s.err = msg
				}
				a.settleTurn(s)
			})
			return
		}
	}
}

func (a *app) handleAgentEvent(s *chatState, ev agent.Event) {
	switch ev.Type {
	case "agent_start":
		s.busy = true
	case "message_start":
		if ev.Message.Role == "assistant" {
			a.finishStreaming(s)
			m := &chatMessage{Role: "assistant", TS: time.Now(), ID: s.nextID + 1}
			s.nextID++
			s.messages = append(s.messages, m)
			s.streaming = m
		}
	case "message_update":
		if ev.Update.Type == "text_delta" && s.streaming != nil {
			s.streaming.Text += ev.Update.Delta
		}
	case "message_end":
		if ev.Message.Role == "assistant" {
			if s.streaming != nil {
				s.streaming.Text = ev.Message.Text()
				a.finishStreaming(s)
			}
			if ev.Message.ErrorMessage != "" {
				s.err = ev.Message.ErrorMessage
			}
		}
	case "tool_execution_start":
		m := &chatMessage{
			Role: "tool", Tool: ev.ToolName, CallID: ev.ToolID,
			ToolStatus: "running", TS: time.Now(),
		}
		s.nextID++
		m.ID = s.nextID
		s.messages = append(s.messages, m)
		s.byCall[ev.ToolID] = m
	case "tool_execution_end":
		m := s.byCall[ev.ToolID]
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
			if n := a.saveDayEntriesFor(s.day, ev.Result.Details); n > 0 {
				m.ToolDetail = fmt.Sprintf("保存 %d 条", n)
			}
		}
		a.persistChat(s, m)
	case "agent_settled":
		s.busy = false
		a.finishStreaming(s)
		a.settleTurn(s)
	case "auto_retry_start":
		s.progress = "模型重试中…"
	case "auto_retry_end":
		if !ev.Aborted {
			s.progress = ""
		}
	case "extension_error":
		s.err = "扩展：" + string(ev.Raw)
	}
}

// settleTurn releases the slot when a turn ends, records the analysis
// outcome, and lets a queued message run before the next analysis day.
func (a *app) settleTurn(s *chatState) {
	if s == nil || a.turn != s {
		return
	}
	a.turn = nil
	s.busy = false
	s.greetingPending = false
	if s.analysis {
		s.analysis = false
		if s.err == "" {
			// The analysis consumed the material: record the fingerprints
			// before the task is marked done. The task is still running, so
			// the sync this triggers leaves it alone.
			a.markAnalyzed(s.day)
		}
		a.finishAnalysisTask(s.day, s.err)
	}
	a.pumpConversation()
}

func (a *app) finishStreaming(s *chatState) {
	m := s.streaming
	if m == nil {
		return
	}
	s.streaming = nil
	if strings.TrimSpace(m.Text) == "" {
		// A turn that only called tools has no text bubble.
		a.removeMessage(s, m)
		return
	}
	m.Done = true
	a.persistChat(s, m)
}

func (a *app) removeMessage(s *chatState, m *chatMessage) {
	for i, x := range s.messages {
		if x == m {
			s.messages = append(s.messages[:i], s.messages[i+1:]...)
			return
		}
	}
}

func (a *app) stopAgent(s *chatState) {
	if s == nil || s.client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := s.client
	go func() {
		_ = c.Abort(ctx)
	}()
}

func (a *app) closeAgent(s *chatState) {
	if s == nil {
		return
	}
	// Drop the handle first, so the consume goroutine's Done path ignores
	// the close instead of settling a turn that is already released.
	c, cancel := s.client, s.cancel
	s.client = nil
	s.ctx = nil
	s.cancel = nil
	if cancel != nil {
		cancel()
	}
	if c != nil {
		_ = c.Close()
	}
}
