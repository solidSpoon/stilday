package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakePi speaks just enough RPC for the client: one prompt, a streamed
// reply, a tool result, and a settled run.
const fakePi = `#!/bin/sh
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  case "$line" in
    *'"type":"prompt"'*)
      printf '%s\n' '{"type":"agent_start"}'
      printf '%s\n' '{"type":"message_start","message":{"role":"assistant","content":[]}}'
      printf '%s\n' '{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"hello "}}'
      printf '%s\n' '{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"world"}}'
      printf '%s\n' '{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"hello world"}],"usage":{"input":12,"output":2,"totalTokens":14}}}'
      printf '%s\n' '{"type":"tool_execution_end","toolCallId":"t1","toolName":"stilday_emit","result":{"content":[{"type":"text","text":"ok"}],"details":{"entries":[{"kind":"observation","summary":"saw a cat","photo":3}]}}}'
      printf '%s\n' '{"type":"agent_settled","aborted":false}'
      printf '{"type":"response","id":"%s","command":"prompt","success":true,"data":{"disposition":"started"}}\n' "$id"
      ;;
    *'"type":"abort"'*)
      printf '{"type":"response","id":"%s","command":"abort","success":true}\n' "$id"
      ;;
  esac
done
`

func fakeClient(t *testing.T) *Client {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "pi")
	if err := os.WriteFile(bin, []byte(fakePi), 0o755); err != nil {
		t.Fatal(err)
	}
	c, err := Start(context.Background(), Options{
		Bin:          bin,
		Dir:          dir,
		SessionID:    "stilday-2026-10-23",
		SessionDir:   filepath.Join(dir, "sessions"),
		Extension:    filepath.Join(dir, "ext.ts"),
		SystemPrompt: "system",
		Provider:     "opencode-go",
		Model:        "kimi-k3",
		Tools:        []string{"day_overview", "stilday_emit"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestPromptStreamsAndResults(t *testing.T) {
	c := fakeClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Prompt(ctx, "look at today", nil); err != nil {
		t.Fatal(err)
	}

	var text, toolName string
	var details []byte
	settled := false
	deadline := time.After(5 * time.Second)
	for !settled {
		select {
		case ev := <-c.Events():
			switch ev.Type {
			case "message_end":
				text = ev.Message.Text()
			case "tool_execution_end":
				toolName = ev.ToolName
				details = ev.Result.Details
			case "agent_settled":
				settled = true
			}
		case <-deadline:
			t.Fatal("no agent_settled event")
		}
	}
	if text != "hello world" {
		t.Errorf("assistant text %q", text)
	}
	if toolName != "stilday_emit" {
		t.Errorf("tool %q", toolName)
	}
	if !strings.Contains(string(details), "saw a cat") {
		t.Errorf("tool details %s", details)
	}
}

func TestArgsCarrySessionAndTools(t *testing.T) {
	c := fakeClient(t)
	argv := strings.Join(c.Argv(), " ")
	for _, want := range []string{"--mode rpc", "--session-id stilday-2026-10-23", "--session-dir", "-e", "--provider opencode-go", "--model kimi-k3", "--tools day_overview,stilday_emit", "--append-system-prompt"} {
		if !strings.Contains(argv, want) {
			t.Errorf("argv %q does not contain %q", argv, want)
		}
	}
}

func TestAbort(t *testing.T) {
	c := fakeClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Abort(ctx); err != nil {
		t.Fatal(err)
	}
}
