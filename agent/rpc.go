// Package agent runs Pi as a child process and speaks its RPC protocol:
// JSONL commands on stdin, JSONL responses and session events on stdout.
//
// Stilday never embeds Pi. The child process owns credentials, tools and
// the model session; this package only starts it, prompts it, and turns its
// event stream into values the app understands.
package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Image is an image attached to a prompt, base64 encoded as Pi expects.
type Image struct {
	Type     string `json:"type"`
	Data     string `json:"data"`
	MIMEType string `json:"mimeType"`
}

// NewImage makes a prompt image from encoded bytes.
func NewImage(data []byte, mimeType string) Image {
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	return Image{Type: "image", Data: base64.StdEncoding.EncodeToString(data), MIMEType: mimeType}
}

// Options configure the Pi child process.
type Options struct {
	// Bin is the pi executable, "pi" by default.
	Bin string
	// Dir is the working directory of the child, and of its tools.
	Dir string
	// Env is added to the inherited environment.
	Env []string
	// SessionID names the Pi session, so a day resumes where it stopped.
	SessionID string
	// SessionDir stores Pi's session files.
	SessionDir string
	// Extension is a TypeScript extension to load, with no other
	// extensions discovered or configured.
	Extension string
	// SystemPrompt appends to Pi's system prompt: text or a file path.
	SystemPrompt string
	// Provider and Model select a model, as Pi's --provider and --model.
	Provider string
	Model    string
	// Tools is the allowlist of tools, extensions included.
	Tools []string
	// NoTools disables all tools for a one-shot, text-only run.
	NoTools bool
}

// Event is one record of Pi's session event stream. Fields not used by the
// app are left zero.
type Event struct {
	Type     string          `json:"type"`
	Message  Message         `json:"message"`
	Update   AssistantEvent  `json:"assistantMessageEvent"`
	ToolID   string          `json:"toolCallId"`
	ToolName string          `json:"toolName"`
	Args     json.RawMessage `json:"args"`
	Result   ToolResult      `json:"result"`
	IsError  bool            `json:"isError"`
	Aborted  bool            `json:"aborted"`

	// Raw is the record as Pi wrote it, for logging.
	Raw json.RawMessage `json:"-"`
}

// Message is a Pi message. Only the parts the app shows are decoded.
type Message struct {
	Role         string    `json:"role"`
	Content      []Content `json:"content"`
	StopReason   string    `json:"stopReason"`
	ErrorMessage string    `json:"errorMessage"`
	Usage        Usage     `json:"usage"`
}

// Text joins the text content blocks of a message.
func (m Message) Text() string {
	var b strings.Builder
	for _, c := range m.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

// Content is one block of a message or a tool result.
type Content struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
}

// AssistantEvent is a streaming update of an assistant message.
type AssistantEvent struct {
	Type         string `json:"type"`
	ContentIndex int    `json:"contentIndex"`
	Delta        string `json:"delta"`
}

// ToolResult is what a tool returned.
type ToolResult struct {
	Content []Content       `json:"content"`
	Details json.RawMessage `json:"details"`
}

// Usage is the token and cost accounting Pi reports.
type Usage struct {
	Input       int `json:"input"`
	Output      int `json:"output"`
	CacheRead   int `json:"cacheRead"`
	CacheWrite  int `json:"cacheWrite"`
	TotalTokens int `json:"totalTokens"`
	Cost        struct {
		Total float64 `json:"total"`
	} `json:"cost"`
}

// Response is Pi's reply to a command.
type Response struct {
	Type    string          `json:"type"`
	ID      string          `json:"id"`
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
}

// Client is a running Pi RPC process.
type Client struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	argv  []string

	events    chan Event
	responses chan Response
	done      chan struct{}

	mu     sync.Mutex
	nextID int

	errMu  sync.Mutex
	stderr bytes.Buffer
	err    error

	closeOnce sync.Once
}

// Start launches Pi in RPC mode.
func Start(ctx context.Context, o Options) (*Client, error) {
	bin := o.Bin
	if bin == "" {
		bin = "pi"
	}
	args := []string{"--mode", "rpc"}
	if o.SessionID != "" {
		args = append(args, "--session-id", o.SessionID)
	}
	if o.SessionDir != "" {
		if err := os.MkdirAll(o.SessionDir, 0o755); err != nil {
			return nil, err
		}
		args = append(args, "--session-dir", o.SessionDir)
	}
	// This app loads exactly one extension: no discovered, configured or
	// built-in ones, and no MCP servers.
	args = append(args, "-ne", "--no-mcp", "--no-skills", "--no-prompt-templates", "--no-context-files")
	if o.Extension != "" {
		args = append(args, "-e", o.Extension)
	}
	if o.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", o.SystemPrompt)
	}
	// Pi requires a model with a provider.
	if o.Provider != "" && o.Model != "" {
		args = append(args, "--provider", o.Provider)
	}
	if o.Model != "" {
		args = append(args, "--model", o.Model)
	}
	if o.NoTools {
		args = append(args, "--no-tools")
	} else if len(o.Tools) > 0 {
		args = append(args, "--tools", strings.Join(o.Tools, ","))
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = o.Dir
	cmd.Env = append(os.Environ(), o.Env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	c := &Client{
		cmd:       cmd,
		stdin:     stdin,
		argv:      append([]string{bin}, args...),
		events:    make(chan Event, 1024),
		responses: make(chan Response, 32),
		done:      make(chan struct{}),
	}
	cmd.Stderr = &c.stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go c.read(stdout)
	return c, nil
}

// Argv is the command line the client started, for diagnostics.
func (c *Client) Argv() []string { return c.argv }

// Events is the session event stream. It is not closed: Done reports the
// end of the process.
func (c *Client) Events() <-chan Event { return c.events }

// Done is closed when Pi's stdout ends.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err is why the event stream ended, if it was not shut down.
func (c *Client) Err() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	return c.err
}

// Stderr is what Pi wrote to its diagnostics stream.
func (c *Client) Stderr() string {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	return c.stderr.String()
}

func (c *Client) read(stdout io.Reader) {
	defer close(c.done)
	dec := json.NewDecoder(stdout)
	for {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			if err != io.EOF && !errors.Is(err, context.Canceled) {
				c.errMu.Lock()
				c.err = err
				c.errMu.Unlock()
			}
			return
		}
		var head struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &head) != nil {
			continue
		}
		if head.Type == "response" {
			var r Response
			if json.Unmarshal(raw, &r) == nil {
				select {
				case c.responses <- r:
				case <-c.done:
					return
				}
			}
			continue
		}
		var ev Event
		if err := json.Unmarshal(raw, &ev); err != nil {
			continue
		}
		ev.Raw = raw
		select {
		case c.events <- ev:
		case <-c.done:
			return
		}
	}
}

// Prompt sends a user message. It returns once Pi accepts the prompt;
// events continue on Events.
func (c *Client) Prompt(ctx context.Context, text string, images []Image) error {
	cmd := map[string]any{"type": "prompt", "message": text}
	if len(images) > 0 {
		cmd["images"] = images
	}
	id, err := c.command(cmd)
	if err != nil {
		return err
	}
	return c.wait(ctx, id)
}

// Steer queues a message into the run in progress.
func (c *Client) Steer(ctx context.Context, text string) error {
	id, err := c.command(map[string]any{"type": "prompt", "message": text, "streamingBehavior": "steer"})
	if err != nil {
		return err
	}
	return c.wait(ctx, id)
}

// Abort stops the run in progress.
func (c *Client) Abort(ctx context.Context) error {
	id, err := c.command(map[string]any{"type": "abort"})
	if err != nil {
		return err
	}
	return c.wait(ctx, id)
}

// GetState asks Pi for its current session state.
func (c *Client) GetState(ctx context.Context) (json.RawMessage, error) {
	id, err := c.command(map[string]any{"type": "get_state"})
	if err != nil {
		return nil, err
	}
	return c.waitData(ctx, id)
}

func (c *Client) command(v map[string]any) (string, error) {
	c.mu.Lock()
	c.nextID++
	id := fmt.Sprintf("stilday-%d", c.nextID)
	c.mu.Unlock()
	v["id"] = id
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	b = append(b, '\n')
	if _, err := c.stdin.Write(b); err != nil {
		return "", err
	}
	return id, nil
}

func (c *Client) wait(ctx context.Context, id string) error {
	_, err := c.waitData(ctx, id)
	return err
}

func (c *Client) waitData(ctx context.Context, id string) (json.RawMessage, error) {
	timer := time.NewTimer(60 * time.Second)
	defer timer.Stop()
	for {
		select {
		case r := <-c.responses:
			if r.ID != id {
				continue
			}
			if !r.Success {
				return nil, errors.New(r.Error)
			}
			return r.Data, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c.done:
			if err := c.Err(); err != nil {
				return nil, err
			}
			return nil, errors.New("pi exited")
		case <-timer.C:
			return nil, errors.New("pi did not answer the command")
		}
	}
}

// Close shuts Pi down, killing it if it does not exit promptly.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		_ = c.stdin.Close()
		select {
		case <-c.done:
		case <-time.After(3 * time.Second):
			if c.cmd.Process != nil {
				_ = c.cmd.Process.Kill()
			}
		}
		_ = c.cmd.Wait()
	})
	return nil
}
