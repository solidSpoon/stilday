package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"stilday/internal/artifact"
)

// dayLogFixture writes a few entries into a temporary storage root.
func dayLogFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	store := artifact.Open(root)
	at := func(day string, hhmm string) time.Time {
		d, err := artifact.ParseDayKey(day)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := time.Parse("15:04", hhmm)
		if err != nil {
			t.Fatal(err)
		}
		return time.Date(d.Year(), d.Month(), d.Day(), parsed.Hour(), parsed.Minute(), 0, 0, time.Local)
	}
	for _, e := range []artifact.Entry{
		{OccurredAt: at("2026-10-01", "12:30"), Day: "2026-10-01", Kind: "food", Summary: "在苏州吃了一碗面"},
		{OccurredAt: at("2026-10-02", "21:00"), Day: "2026-10-02", Kind: "health", Summary: "血压 145，加了坎地沙坦"},
		{OccurredAt: at("2026-10-03", "10:00"), Day: "2026-10-03", Kind: "plan", Summary: "10 月 6 日 G1256 长春西到常熟"},
		{OccurredAt: at("2026-10-03", "08:00"), Day: "2026-10-03", Kind: "observation", Summary: "早上刷到一条帖子"},
	} {
		if err := store.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestSearchDayLog(t *testing.T) {
	root := dayLogFixture(t)
	all, err := searchDayLog(root, searchArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if all.Count != 4 || len(all.Entries) != 4 {
		t.Fatalf("all: %+v", all)
	}
	if all.Entries[0].Summary != "10 月 6 日 G1256 长春西到常熟" {
		t.Fatalf("newest first: %+v", all.Entries[0])
	}

	text, err := searchDayLog(root, searchArgs{Text: "苏州"})
	if err != nil || text.Count != 1 || text.Entries[0].Kind != "food" {
		t.Fatalf("text: %+v, %v", text, err)
	}

	kinds, err := searchDayLog(root, searchArgs{Kinds: []string{"health", "plan"}})
	if err != nil || kinds.Count != 2 {
		t.Fatalf("kinds: %+v, %v", kinds, err)
	}

	ranged, err := searchDayLog(root, searchArgs{From: "2026-10-02", To: "2026-10-02"})
	if err != nil || ranged.Count != 1 || ranged.Entries[0].Kind != "health" {
		t.Fatalf("range: %+v, %v", ranged, err)
	}

	asc, err := searchDayLog(root, searchArgs{Order: "asc", Limit: 2})
	if err != nil || asc.Count != 4 || len(asc.Entries) != 2 {
		t.Fatalf("asc+limit: %+v, %v", asc, err)
	}
	if asc.Entries[0].Summary != "在苏州吃了一碗面" {
		t.Fatalf("oldest first: %+v", asc.Entries[0])
	}

	if _, err := searchDayLog(root, searchArgs{From: "10/01"}); err == nil {
		t.Fatal("a malformed date was accepted")
	}
}

// The endpoint answers a plain MCP client over HTTP: list the tool, call
// it, and get the day log back.
func TestMCPHTTPEndpoint(t *testing.T) {
	root := dayLogFixture(t)
	srv := httptest.NewServer(mcpMux(root, "test"))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL + mcpPath}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].Name != "search_events" {
		t.Fatalf("tools: %+v", tools.Tools)
	}
	if !strings.Contains(tools.Tools[0].Description, "主人的日常记录") {
		t.Errorf("tool description: %q", tools.Tools[0].Description)
	}

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search_events",
		Arguments: map[string]any{"text": "血压"},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	var text strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	if !strings.Contains(text.String(), "坎地沙坦") {
		t.Fatalf("result: %s", text.String())
	}
}

// The endpoint says what it offers, without naming the material behind the
// records.
func TestMCPOffersOwnerInformation(t *testing.T) {
	srv := httptest.NewServer(mcpMux(dayLogFixture(t), "test"))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL + mcpPath}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	info := session.InitializeResult()
	if info == nil {
		t.Fatal("no initialize result")
	}
	for _, banned := range []string{"照片", "图片", "截图", "对话"} {
		if strings.Contains(info.Instructions, banned) || strings.Contains(info.ServerInfo.Description, banned) {
			t.Errorf("the server mentions %q: %+v", banned, info)
		}
	}
	if !strings.Contains(info.ServerInfo.Description, "主人的日常记录") {
		t.Errorf("server description: %q", info.ServerInfo.Description)
	}
}
