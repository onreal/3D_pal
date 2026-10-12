package web_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/margarit/printpipe/internal/assembly"
	"github.com/margarit/printpipe/internal/bridge"
	"github.com/margarit/printpipe/internal/events"
	"github.com/margarit/printpipe/internal/mcpserver"
	"github.com/margarit/printpipe/internal/pipeline/pipelinetest"
	"github.com/margarit/printpipe/internal/sessions"
	"github.com/margarit/printpipe/internal/web"
)

type multi struct {
	srv *httptest.Server
	env *pipelinetest.Env
	reg *sessions.Registry
}

func setupMulti(t *testing.T) *multi {
	env := pipelinetest.New(t)
	reg := sessions.NewRegistry()
	server := mcpserver.New(env.Svc, "test", reg)
	mcpHTTP := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	srv := httptest.NewServer(web.Handler(env.Svc, env.Config, events.NewHub(), fstest.MapFS{}, web.Options{MCP: mcpHTTP, Sessions: reg}))
	t.Cleanup(srv.Close)
	return &multi{srv: srv, env: env, reg: reg}
}

func (m *multi) connect(t *testing.T, client string) *mcp.ClientSession {
	t.Helper()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: client, Version: "1.0"}, nil).
		Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: m.srv.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func (m *multi) get(t *testing.T, path string, v any) int {
	t.Helper()
	resp, err := http.Get(m.srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if v != nil {
		json.Unmarshal(b, v)
	}
	return resp.StatusCode
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func compile(t *testing.T, cs *mcp.ClientSession, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "compile_model", Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestManySessionsAtOnce(t *testing.T) {
	m := setupMulti(t)
	code := m.connect(t, "claude-code")
	desk := m.connect(t, "claude-desktop")
	waitFor(t, "two open sessions", func() bool { return m.reg.OpenCount() == 2 })

	// Both compile the same project at the same time: each gets its own version.
	var wg sync.WaitGroup
	results := make([]*mcp.CallToolResult, 2)
	for i, cs := range []*mcp.ClientSession{code, desk} {
		wg.Add(1)
		go func(i int, cs *mcp.ClientSession) {
			defer wg.Done()
			results[i] = compile(t, cs, map[string]any{"project": "cube", "scad_code": pipelinetest.Cube})
		}(i, cs)
	}
	wg.Wait()
	for _, r := range results {
		if r.IsError {
			t.Fatal(r.Content[0].(*mcp.TextContent).Text)
		}
	}
	vs, _ := m.env.Svc.Versions("cube")
	if len(vs) != 2 {
		t.Fatalf("want versions 1 and 2, got %d", len(vs))
	}

	var list []sessions.Info
	m.get(t, "/api/sessions", &list)
	clients := map[string]sessions.Info{}
	for _, s := range list {
		clients[s.Client] = s
	}
	cc, cd := clients["claude-code"], clients["claude-desktop"]
	if !cc.Open || cc.Transport != sessions.HTTP || cc.Calls != 1 || cc.Project != "cube" || cc.LastTool != "compile_model" ||
		cc.RemoteAddr == "" || cc.UserAgent == "" || cc.ID != code.ID() || !cd.Open {
		t.Fatalf("sessions: %+v", list)
	}

	// Each session's run log holds only its own calls, and names its client.
	var detail struct {
		Session sessions.Info `json:"session"`
		Runs    []struct {
			Tool, Client, Session string
		} `json:"runs"`
	}
	m.get(t, "/api/sessions/"+code.ID(), &detail)
	if len(detail.Runs) != 1 || detail.Runs[0].Client != "claude-code" || detail.Runs[0].Session != code.ID() {
		t.Fatalf("%+v", detail)
	}
	if m.get(t, "/api/sessions/nope", nil) != 404 {
		t.Fatal("unknown session found")
	}

	// A session that disconnects is listed as closed.
	desk.Close()
	waitFor(t, "desktop session closed", func() bool { i, _ := m.reg.Get(cd.ID); return !i.Open })
}

func TestAdminCanEndASession(t *testing.T) {
	m := setupMulti(t)
	cs := m.connect(t, "claude-code")
	waitFor(t, "session", func() bool { return m.reg.OpenCount() == 1 })
	req, _ := http.NewRequest("DELETE", m.srv.URL+"/api/sessions/"+cs.ID(), nil)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 200 {
		t.Fatalf("%d", resp.StatusCode)
	}
	if i, _ := m.reg.Get(cs.ID()); i.Open {
		t.Fatal("still open")
	}
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 409 {
		t.Fatalf("ending twice: %d", resp.StatusCode)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "list_printers"}); err == nil {
		t.Fatal("an ended session still answered")
	}
}

func TestEventsNameTheSession(t *testing.T) {
	m := setupMulti(t)
	var mu sync.Mutex
	var got []map[string]any
	m.env.Events.Next = func(typ string, v any) {
		if typ == "model" {
			b, _ := json.Marshal(v)
			var e map[string]any
			json.Unmarshal(b, &e)
			mu.Lock()
			got = append(got, e)
			mu.Unlock()
		}
	}
	cs := m.connect(t, "claude-code")
	compile(t, cs, map[string]any{"project": "cube", "scad_code": pipelinetest.Cube})
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0]["session"] != cs.ID() || got[0]["client"] != "claude-code" || got[0]["project"] != "cube" {
		t.Fatalf("%+v", got)
	}
}

func TestBridgeJoinsTheSameServer(t *testing.T) {
	m := setupMulti(t)
	clientToBridge, bridgeIn := io.Pipe()
	bridgeOut, bridgeToClient := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- bridge.Run(ctx, clientToBridge, bridgeToClient, m.srv.URL+"/mcp", "test") }()

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "claude-desktop", Version: "0.14"}, nil).
		Connect(context.Background(), &mcp.IOTransport{Reader: bridgeOut, Writer: bridgeIn}, nil)
	if err != nil {
		t.Fatal(err)
	}
	res := compile(t, cs, map[string]any{"project": "box", "scad_code": pipelinetest.Cube})
	if res.IsError {
		t.Fatal(res.Content[0].(*mcp.TextContent).Text)
	}
	var list []sessions.Info
	m.get(t, "/api/sessions", &list)
	if len(list) != 1 || list[0].Client != "claude-desktop" || list[0].Version != "0.14" || list[0].Transport != sessions.Bridge ||
		!strings.HasPrefix(list[0].UserAgent, "printpipe-bridge/") || list[0].Project != "box" {
		t.Fatalf("%+v", list)
	}
	// The stdio client going away ends the bridge and its HTTP session.
	cs.Close()
	bridgeIn.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("bridge did not stop")
	}
	waitFor(t, "bridged session closed", func() bool { return m.reg.OpenCount() == 0 })
}

func TestAssemblyEndpoints(t *testing.T) {
	env := pipelinetest.New(t)
	asm := assembly.New(env.SCAD, env.Projects, nil)
	srv := httptest.NewServer(web.Handler(env.Svc, env.Config, events.NewHub(), fstest.MapFS{}, web.Options{Assembly: asm}))
	defer srv.Close()
	env.Svc.Compile(context.Background(), "box", pipelinetest.Cube, nil)
	get := func(path string) (int, []byte) {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	code, body := get("/api/projects/box/versions/1/assembly")
	if code != 202 || !strings.Contains(string(body), `"running"`) {
		t.Fatalf("%d %s", code, body)
	}
	v, _ := env.Svc.Version("box", 1)
	asm.Wait(v)
	code, body = get("/api/projects/box/versions/1/assembly")
	var r assembly.Result
	json.Unmarshal(body, &r)
	if code != 200 || r.Status != "done" || r.Mode != assembly.Pieces || len(r.Parts) != 2 || r.Parts[0].Max.Z != 2 {
		t.Fatalf("%d %+v", code, r)
	}
	if code, body = get("/api/projects/box/versions/1/assembly/parts/1"); code != 200 || len(body) != 84+12*50 {
		t.Fatalf("part STL: %d %d bytes", code, len(body))
	}
	for path, want := range map[string]int{
		"/api/projects/box/versions/1/assembly/parts/x": 400,
		"/api/projects/box/versions/1/assembly/parts/9": 404,
		"/api/projects/box/versions/7/assembly":         404,
		"/api/projects/..%2Fx/versions/1/assembly":      400,
	} {
		if code, _ := get(path); code != want {
			t.Errorf("%s: %d, want %d", path, code, want)
		}
	}
	plain := httptest.NewServer(web.Handler(env.Svc, env.Config, events.NewHub(), fstest.MapFS{}, web.Options{}))
	defer plain.Close()
	if resp, _ := http.Get(plain.URL + "/api/projects/box/versions/1/assembly"); resp.StatusCode != 404 {
		t.Fatalf("without the service: %d", resp.StatusCode)
	}
}
