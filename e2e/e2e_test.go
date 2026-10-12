//go:build e2e

// Package e2e drives the real Docker image over MCP, the way agents do: one long-running
// server container, a Claude Code-like client over HTTP and a Claude Desktop-like client
// through the stdio bridge at the same time, real OpenSCAD and Bambu Studio, and the
// simulated printer.
//
//	docker build -t print-pipeline:dev .
//	go test -tags e2e -v ./e2e/
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const port = "18080"

var base = "http://127.0.0.1:" + port

func image() string {
	if v := os.Getenv("PRINTPIPE_IMAGE"); v != "" {
		return v
	}
	return "print-pipeline:dev"
}

func httpDo(t *testing.T, method, path string, body io.Reader, contentType string) []byte {
	t.Helper()
	req, _ := http.NewRequest(method, base+path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Origin", base)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, out)
	}
	return out
}

func uploadProfile(t *testing.T, kind, path string) string {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", filepath.Base(path))
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(b)
	mw.Close()
	httpDo(t, "POST", "/api/profiles/"+kind, &buf, mw.FormDataContentType())
	return filepath.Base(path)
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	start := time.Now()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var text []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text = append(text, tc.Text)
		}
	}
	t.Logf("%s (%s) error=%v: %.400s", name, time.Since(start).Round(time.Millisecond), res.IsError, strings.Join(text, " | "))
	return res, strings.Join(text, "\n")
}

func structured(t *testing.T, res *mcp.CallToolResult, v any) {
	t.Helper()
	b, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
}

const container = "printpipe-e2e"

func TestPromptToPrintInDocker(t *testing.T) {
	data, work := t.TempDir(), t.TempDir()
	exec.Command("docker", "rm", "-f", container).Run()
	if out, err := exec.Command("docker", "run", "-d", "--name", container, "--platform", "linux/amd64",
		"-p", "127.0.0.1:"+port+":8080", "-v", data+":/data", "-v", work+":/work", image()).CombinedOutput(); err != nil {
		t.Fatalf("start server: %v %s", err, out)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", container).Run() })

	// The viewer comes up with the MCP session.
	deadline := time.Now().Add(60 * time.Second)
	for {
		if resp, err := http.Get(base + "/api/health"); err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("viewer did not come up")
		}
		time.Sleep(500 * time.Millisecond)
	}
	if page := httpDo(t, "GET", "/", nil, ""); !bytes.Contains(page, []byte("<div id=\"app\">")) {
		t.Fatalf("viewer page: %.200s", page)
	}

	// Two agents at once: Claude Code over HTTP, Claude Desktop through the stdio bridge.
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "2.1.0"}, nil).
		Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: base + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	bridgeCmd := exec.Command("docker", "exec", "-i", container, "printpipe", "bridge")
	bridgeCmd.Stderr = os.Stderr
	desk, err := mcp.NewClient(&mcp.Implementation{Name: "claude-desktop", Version: "0.14.2"}, nil).
		Connect(context.Background(), &mcp.CommandTransport{Command: bridgeCmd}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Settings: real Bambu Studio slicing with A1 profiles, the simulated printer, a filament price.
	machine := uploadProfile(t, "machine", "../testdata/profiles/machine.json")
	process := uploadProfile(t, "process", "../testdata/profiles/process.json")
	filament := uploadProfile(t, "filament", "../testdata/profiles/filament.json")
	var set map[string]any
	json.Unmarshal(httpDo(t, "GET", "/api/settings", nil, ""), &set)
	set["printers"] = []map[string]any{{"id": "sim", "name": "Simulated A1", "kind": "fake", "default": true,
		"config":   map[string]string{"print_seconds": "30", "slicer": "bambustudio"},
		"profiles": map[string]string{"machine": machine, "process": process, "filament": filament}}}
	for _, p := range set["profiles"].([]any) {
		if p.(map[string]any)["kind"] == "filament" {
			p.(map[string]any)["price_per_kg"] = 25
		}
	}
	b, _ := json.Marshal(set)
	httpDo(t, "PUT", "/api/settings", bytes.NewReader(b), "application/json")
	httpDo(t, "POST", "/api/printers/sim/test", nil, "")

	cube, _ := os.ReadFile("../testdata/cube.scad")
	res, text := call(t, cs, "compile_model", map[string]any{"project": "test-cube", "scad_code": string(cube)})
	if res.IsError || !strings.Contains(text, "20.0 × 20.0 × 20.0 mm") {
		t.Fatalf("compile: %s", text)
	}
	images := 0
	for _, c := range res.Content {
		if im, ok := c.(*mcp.ImageContent); ok && bytes.HasPrefix(im.Data, []byte("\x89PNG")) {
			images++
		}
	}
	if images != 2 {
		t.Fatalf("want 2 preview images, got %d", images)
	}

	res, text = call(t, cs, "compile_model", map[string]any{"project": "test-cube", "params": map[string]any{"size": 25}})
	if res.IsError || !strings.Contains(text, "version 2") || !strings.Contains(text, "25.0 × 25.0 × 25.0 mm") {
		t.Fatalf("param change: %s", text)
	}

	res, text = call(t, cs, "estimate_print", map[string]any{"project": "test-cube", "versions": []int{1, 2}})
	if res.IsError {
		t.Fatalf("estimate: %s", text)
	}
	var est struct {
		Estimates []struct {
			Version int     `json:"version"`
			Minutes int     `json:"minutes"`
			Grams   float64 `json:"grams"`
			Layers  int     `json:"layers"`
			Cost    float64 `json:"cost"`
		} `json:"estimates"`
	}
	structured(t, res, &est)
	if len(est.Estimates) != 2 || est.Estimates[0].Minutes < 5 || est.Estimates[0].Minutes > 60 || est.Estimates[0].Grams <= 0 ||
		est.Estimates[0].Layers < 50 || est.Estimates[0].Cost <= 0 || est.Estimates[1].Grams <= est.Estimates[0].Grams {
		t.Fatalf("implausible estimates: %+v", est)
	}
	start := time.Now()
	call(t, cs, "estimate_print", map[string]any{"project": "test-cube", "versions": []int{1, 2}})
	if time.Since(start) > 3*time.Second {
		t.Fatalf("second estimate took %s: not cached", time.Since(start))
	}

	res, text = call(t, cs, "request_approval", map[string]any{"project": "test-cube", "version": 1})
	if res.IsError {
		t.Fatalf("request_approval: %s", text)
	}
	var approval struct {
		JobID string `json:"job_id"`
	}
	structured(t, res, &approval)

	res, text = call(t, cs, "print_job", map[string]any{"job_id": approval.JobID})
	if !res.IsError || !strings.Contains(text, "Not approved yet") {
		t.Fatalf("printed without approval: %s", text)
	}

	httpDo(t, "POST", "/api/jobs/"+approval.JobID+"/approve", nil, "") // the user's click

	res, text = call(t, cs, "print_job", map[string]any{"job_id": approval.JobID})
	if res.IsError || !strings.Contains(text, "Printing on Simulated A1") {
		t.Fatalf("print: %s", text)
	}
	res, text = call(t, cs, "print_job", map[string]any{"job_id": approval.JobID})
	if !res.IsError || !strings.Contains(text, "already sent") {
		t.Fatalf("second print: %s", text)
	}

	// The desktop session works on its own project meanwhile.
	if res, text := call(t, desk, "compile_model", map[string]any{"project": "desk-part", "scad_code": "size = 10; // [5:50]\ncube(size);\n"}); res.IsError {
		t.Fatalf("desktop compile: %s", text)
	}
	var list []struct {
		ID, Client, Transport, Project string
		Calls                          int
		Open                           bool
	}
	json.Unmarshal(httpDo(t, "GET", "/api/sessions", nil, ""), &list)
	byClient := map[string]int{}
	for i, s := range list {
		byClient[s.Client] = i
	}
	code, desktop := list[byClient["claude-code"]], list[byClient["claude-desktop"]]
	if len(list) != 2 || !code.Open || code.Transport != "http" || code.Calls < 7 || code.Project != "test-cube" ||
		!desktop.Open || desktop.Transport != "bridge" || desktop.Calls != 1 || desktop.Project != "desk-part" {
		t.Fatalf("sessions: %+v", list)
	}
	var detail struct {
		Runs []struct{ Client, Tool string }
	}
	json.Unmarshal(httpDo(t, "GET", "/api/sessions/"+desktop.ID, nil, ""), &detail)
	if len(detail.Runs) != 1 || detail.Runs[0].Client != "claude-desktop" {
		t.Fatalf("desktop runs: %+v", detail.Runs)
	}
	desk.Close()
	deadline = time.Now().Add(10 * time.Second)
	for {
		json.Unmarshal(httpDo(t, "GET", "/api/sessions", nil, ""), &list)
		closed := false
		for _, s := range list {
			if s.Client == "claude-desktop" && !s.Open {
				closed = true
			}
		}
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("bridged session not closed: %+v", list)
		}
		time.Sleep(200 * time.Millisecond)
	}

	for _, f := range []string{"model.scad", "model.stl", "preview.png", "job.gcode.3mf", "params.json", "run.log"} {
		if _, err := os.Stat(filepath.Join(work, "test-cube", f)); err != nil {
			t.Errorf("project folder is missing %s", f)
		}
	}
	if _, err := os.Stat(filepath.Join(data, "printpipe.db")); err != nil {
		t.Error("no database on the data volume")
	}
	logs, _ := os.ReadFile(filepath.Join(data, "logs", "printpipe.log"))
	if bytes.Contains(logs, []byte("access_code")) {
		t.Error("the log mentions the access code")
	}
	fmt.Println("estimate v1:", est.Estimates[0], "v2:", est.Estimates[1])
}
