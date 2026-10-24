package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"stilday/internal/artifact"
)

// mcpPath is where the MCP endpoint lives: http://127.0.0.1:<port>/mcp.
const mcpPath = "/mcp"

// mcpDefaultPort is the port the endpoint uses until the settings say
// otherwise: 7845 is "STIL" on a phone keypad, and is far from the ports
// tools tend to squat on.
const mcpDefaultPort = "7845"

// mcpService is the local MCP server: a URL other assistants paste in, a
// stateless streamable HTTP endpoint, and one read-only query tool.
type mcpService struct {
	mu   sync.Mutex
	srv  *http.Server
	url  string
	port string
	root string
	err  string
}

// reload starts, stops or moves the endpoint to match cfg. It is called at
// startup and after the settings are saved.
func (m *mcpService) reload(cfg config, version string) {
	port := strings.TrimSpace(cfg.MCPPort)
	if port == "" {
		port = mcpDefaultPort
	}
	root := cfg.artifactsRoot()
	want := cfg.MCPEnabled && root != ""

	m.mu.Lock()
	defer m.mu.Unlock()
	if want && m.srv != nil && m.root == root && m.port == port {
		return
	}
	m.stopLocked()
	if !want {
		return
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		m.err = "监听 127.0.0.1:" + port + "：" + err.Error()
		return
	}
	srv := &http.Server{Handler: mcpMux(root, version)}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			m.mu.Lock()
			m.err = err.Error()
			m.mu.Unlock()
		}
	}()
	m.srv, m.url, m.port, m.root, m.err = srv, "http://127.0.0.1:"+port+mcpPath, port, root, ""
}

// stop ends the endpoint, keeping the struct usable for a later reload.
func (m *mcpService) stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
}

func (m *mcpService) stopLocked() {
	if m.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = m.srv.Shutdown(ctx)
		cancel()
	}
	m.srv, m.url, m.port, m.root, m.err = nil, "", "", "", ""
}

// status reports the running endpoint, for the settings page.
func (m *mcpService) status() (url, errText string, running bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.url, m.err, m.srv != nil
}

// mcpMux mounts the endpoint. The storage root is captured, so a save that
// changes it builds a new server.
func mcpMux(root, version string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(mcpPath, newMCPHandler(root, version))
	return mux
}

// newMCPHandler builds the MCP server: what it offers, and the one tool
// that reads the day log.
func newMCPHandler(root, version string) http.Handler {
	if version == "" {
		version = "dev"
	}
	server := mcp.NewServer(&mcp.Implementation{
		Name:        "stilday-daily-log",
		Title:       "Stilday 主人的日常记录",
		Description: "提供主人的日常记录：做过的事、行程安排、吃喝、身体、兴趣和观察。",
		Version:     version,
	}, &mcp.ServerOptions{
		Instructions: "这里是主人的个人日常记录，按天归档。用 search_events 按日期范围、类型或关键词查询；每条记录有一个发生时间、一个类型和一句话说明。",
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_events",
		Title:       "查询日常记录",
		Description: "查询主人的日常记录。可以按日期范围、类型和关键词过滤，返回发生时间、类型和说明。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args searchArgs) (*mcp.CallToolResult, searchResult, error) {
		result, err := searchDayLog(root, args)
		if err != nil {
			return nil, searchResult{}, err
		}
		return nil, result, nil
	})
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
	})
}

// searchArgs is the query's shape: the fields a day's entries have, and the
// few filters that are useful on them.
type searchArgs struct {
	From  string   `json:"from,omitempty" jsonschema:"起始日期，YYYY-MM-DD，含这一天；不填就是最早"`
	To    string   `json:"to,omitempty" jsonschema:"结束日期，YYYY-MM-DD，含这一天；不填就是最晚"`
	Kinds []string `json:"kinds,omitempty" jsonschema:"只要这些类型：observation、activity、food、health、plan、interest；空就是全部"`
	Text  string   `json:"text,omitempty" jsonschema:"在说明里找这个词，不区分大小写"`
	Order string   `json:"order,omitempty" jsonschema:"desc 从新到旧（默认），asc 从旧到新"`
	Limit int      `json:"limit,omitempty" jsonschema:"最多返回几条，默认 50，最多 500"`
}

type searchResult struct {
	Count   int           `json:"count" jsonschema:"匹配的记录总数，可能多于 entries 的条数"`
	Entries []searchEntry `json:"entries" jsonschema:"按指定顺序排列的记录"`
}

type searchEntry struct {
	OccurredAt string `json:"occurred_at" jsonschema:"事情发生的时间，RFC 3339"`
	Day        string `json:"day" jsonschema:"记录归到哪一天，YYYY-MM-DD"`
	Kind       string `json:"kind" jsonschema:"类型：observation、activity、food、health、plan、interest"`
	Summary    string `json:"summary" jsonschema:"一句话说明"`
}

// searchDayLog reads every year file and filters it. The data is one file
// per year, so a full read is cheap and stays correct without an index.
func searchDayLog(root string, q searchArgs) (searchResult, error) {
	if root == "" {
		return searchResult{}, errors.New("还没有存储目录")
	}
	entries, err := artifact.Open(root).LoadAll()
	if err != nil {
		return searchResult{}, err
	}
	from, to := strings.TrimSpace(q.From), strings.TrimSpace(q.To)
	if from != "" {
		if _, err := artifact.ParseDayKey(from); err != nil {
			return searchResult{}, fmt.Errorf("from 要写成 YYYY-MM-DD")
		}
	}
	if to != "" {
		if _, err := artifact.ParseDayKey(to); err != nil {
			return searchResult{}, fmt.Errorf("to 要写成 YYYY-MM-DD")
		}
	}
	kinds := make(map[string]bool, len(q.Kinds))
	for _, k := range q.Kinds {
		if k = strings.TrimSpace(k); k != "" {
			kinds[k] = true
		}
	}
	text := strings.ToLower(strings.TrimSpace(q.Text))
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	matches := make([]artifact.Entry, 0, len(entries))
	for _, e := range entries {
		day := e.Day
		if day == "" {
			day = artifact.DayKey(e.OccurredAt)
		}
		if from != "" && day < from {
			continue
		}
		if to != "" && day > to {
			continue
		}
		if len(kinds) > 0 && !kinds[e.Kind] {
			continue
		}
		if text != "" && !strings.Contains(strings.ToLower(e.Summary), text) {
			continue
		}
		matches = append(matches, e)
	}
	asc := strings.EqualFold(q.Order, "asc")
	sort.SliceStable(matches, func(i, j int) bool {
		a, b := matches[i], matches[j]
		if a.OccurredAt.Equal(b.OccurredAt) {
			return a.Summary < b.Summary
		}
		if asc {
			return a.OccurredAt.Before(b.OccurredAt)
		}
		return a.OccurredAt.After(b.OccurredAt)
	})
	result := searchResult{Count: len(matches), Entries: []searchEntry{}}
	if len(matches) > limit {
		matches = matches[:limit]
	}
	for _, e := range matches {
		result.Entries = append(result.Entries, searchEntry{
			OccurredAt: e.OccurredAt.Format(time.RFC3339),
			Day:        e.Day,
			Kind:       e.Kind,
			Summary:    e.Summary,
		})
	}
	return result, nil
}

// mcpPortNumber is the configured port as a number, or the default.
func mcpPortNumber(cfg config) int {
	n, err := strconv.Atoi(strings.TrimSpace(cfg.MCPPort))
	if err != nil || n <= 0 || n > 65535 {
		n, _ = strconv.Atoi(mcpDefaultPort)
	}
	return n
}

// mcpURL is the address the settings page shows and copies.
func mcpURL(cfg config) string {
	return "http://127.0.0.1:" + strconv.Itoa(mcpPortNumber(cfg)) + mcpPath
}
