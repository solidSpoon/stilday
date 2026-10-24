package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// chatRecord is a transcript line in the day folder.
type chatRecord struct {
	TS     string `json:"ts"`
	Role   string `json:"role"`
	Text   string `json:"text,omitempty"`
	Tool   string `json:"tool,omitempty"`
	Status string `json:"status,omitempty"`
	Detail string `json:"detail,omitempty"`
	Images int    `json:"images,omitempty"`
}

// addMessage appends a message and persists it.
func (a *app) addMessage(m *chatMessage) {
	a.chat.nextID++
	m.ID = a.chat.nextID
	if m.TS.IsZero() {
		m.TS = time.Now()
	}
	a.chat.messages = append(a.chat.messages, m)
	a.persistChat(m)
}

func (a *app) persistChat(m *chatMessage) {
	if a.chat.dir == "" || (m.Role == "assistant" && strings.TrimSpace(m.Text) == "") {
		return
	}
	r := chatRecord{
		TS:     m.TS.Format(time.RFC3339),
		Role:   m.Role,
		Text:   m.Text,
		Tool:   m.Tool,
		Status: m.ToolStatus,
		Detail: m.ToolDetail,
		Images: m.Images,
	}
	b, err := json.Marshal(r)
	if err != nil {
		return
	}
	if err := os.MkdirAll(a.chat.dir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(a.chat.dir, "chat.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

func loadTranscript(dir string) []*chatMessage {
	f, err := os.Open(filepath.Join(dir, "chat.jsonl"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []*chatMessage
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var r chatRecord
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		ts, _ := time.Parse(time.RFC3339, r.TS)
		// A stored message is complete: the transcript only keeps finished
		// rows, so every one is done streaming.
		out = append(out, &chatMessage{
			Role: r.Role, Text: r.Text, Tool: r.Tool,
			ToolStatus: r.Status, ToolDetail: r.Detail, Images: r.Images, TS: ts,
			Done: true,
		})
	}
	return out
}
