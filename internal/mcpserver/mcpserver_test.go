package mcpserver_test

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/margarit/printpipe/internal/mcpserver"
	"github.com/margarit/printpipe/internal/pipeline/pipelinetest"
)

var ctx = context.Background()

func connect(t *testing.T) (*mcp.ClientSession, *pipelinetest.Env) {
	t.Helper()
	env := pipelinetest.New(t)
	server := mcpserver.New(env.Svc, "test", nil)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs, env
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error %v", name, err)
	}
	return res
}

func text(res *mcp.CallToolResult) string {
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func structured[T any](t *testing.T, res *mcp.CallToolResult) T {
	t.Helper()
	var out T
	b, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("structured content %s: %v", b, err)
	}
	return out
}

func TestToolsListedAndNoApproveTool(t *testing.T) {
	cs, _ := connect(t)
	list, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
		if strings.Contains(tool.Name, "approve") && tool.Name != "request_approval" {
			t.Fatalf("the agent must not be able to approve: %s", tool.Name)
		}
	}
	sort.Strings(names)
	want := "cancel_print compile_model estimate_print get_model list_printers list_projects print_job printer_status request_approval slice_model"
	if strings.Join(names, " ") != want {
		t.Fatalf("tools: %v", names)
	}
}

func TestCompileReturnsSummaryAndImages(t *testing.T) {
	cs, _ := connect(t)
	res := call(t, cs, "compile_model", map[string]any{"project": "cube", "scad_code": pipelinetest.Cube, "estimate": true})
	if res.IsError {
		t.Fatal(text(res))
	}
	images := 0
	for _, c := range res.Content {
		if im, ok := c.(*mcp.ImageContent); ok && im.MIMEType == "image/png" {
			images++
		}
	}
	if images != 2 || !strings.Contains(text(res), "20.0 × 20.0 × 20.0 mm") || !strings.Contains(text(res), "Estimate:") {
		t.Fatalf("images=%d text=%s", images, text(res))
	}
}

func TestCompileErrorIsAToolError(t *testing.T) {
	cs, _ := connect(t)
	res := call(t, cs, "compile_model", map[string]any{"project": "../etc", "scad_code": "cube(1);"})
	if !res.IsError || !strings.Contains(text(res), "project name") {
		t.Fatalf("got %+v %s", res.IsError, text(res))
	}
}

func TestPrintBeforeApprovalFails(t *testing.T) {
	cs, env := connect(t)
	call(t, cs, "compile_model", map[string]any{"project": "cube", "scad_code": pipelinetest.Cube})
	res := call(t, cs, "request_approval", map[string]any{"project": "cube"})
	if res.IsError {
		t.Fatal(text(res))
	}
	approval := structured[mcpserver.ApprovalOut](t, res)
	if len(approval.JobID) != 8 || !strings.Contains(approval.Message, "Approve") {
		t.Fatalf("%+v", approval)
	}

	res = call(t, cs, "print_job", map[string]any{"job_id": approval.JobID})
	if !res.IsError || !strings.Contains(text(res), "Not approved yet") {
		t.Fatalf("printed without approval: %s", text(res))
	}
	if env.Printer.Uploads() != 0 {
		t.Fatal("printer contacted")
	}

	if _, err := env.Svc.Approve(approval.JobID); err != nil { // the user's click
		t.Fatal(err)
	}
	res = call(t, cs, "print_job", map[string]any{"job_id": approval.JobID})
	if res.IsError {
		t.Fatal(text(res))
	}
	if out := structured[mcpserver.PrintOut](t, res); out.Status.State != "printing" || env.Printer.Starts() != 1 {
		t.Fatalf("%+v", out)
	}
	st := structured[mcpserver.StatusOut](t, call(t, cs, "printer_status", nil))
	if st.Status.State != "printing" {
		t.Fatalf("%+v", st)
	}
	call(t, cs, "cancel_print", nil)
	if st := structured[mcpserver.StatusOut](t, call(t, cs, "printer_status", nil)); st.Status.State != "idle" {
		t.Fatalf("%+v", st)
	}
}

func TestEstimateComparesVersions(t *testing.T) {
	cs, _ := connect(t)
	call(t, cs, "compile_model", map[string]any{"project": "cube", "scad_code": pipelinetest.Cube})
	call(t, cs, "compile_model", map[string]any{"project": "cube", "params": map[string]any{"size": 30}})
	res := call(t, cs, "estimate_print", map[string]any{"project": "cube", "versions": []int{1, 2}})
	if res.IsError {
		t.Fatal(text(res))
	}
	out := structured[mcpserver.EstimateOut](t, res)
	if len(out.Estimates) != 2 || out.Estimates[0].Grams >= out.Estimates[1].Grams || !strings.Contains(out.Summary, "version 2") {
		t.Fatalf("%+v", out)
	}
	off := false
	res = call(t, cs, "slice_model", map[string]any{"project": "cube", "supports": off, "layer_height": 0.12})
	if res.IsError {
		t.Fatal(text(res))
	}
	if out := structured[mcpserver.EstimateOut](t, res); out.Estimates[0].LayerHeight != 0.12 {
		t.Fatalf("%+v", out)
	}
}

func TestGetModelAndListings(t *testing.T) {
	cs, _ := connect(t)
	call(t, cs, "compile_model", map[string]any{"project": "cube", "scad_code": pipelinetest.Cube})
	m := structured[mcpserver.ModelOut](t, call(t, cs, "get_model", map[string]any{"project": "cube"}))
	if m.Version != 1 || !strings.Contains(m.Code, "cube(size)") || len(m.Params) != 1 || m.Params[0].Name != "size" {
		t.Fatalf("%+v", m)
	}
	ps := structured[mcpserver.PrintersOut](t, call(t, cs, "list_printers", nil))
	if len(ps.Printers) != 1 || !ps.Printers[0].Default || ps.Printers[0].Capabilities.BuildVolume.X != 256 {
		t.Fatalf("%+v", ps)
	}
	prj := structured[mcpserver.ProjectsOut](t, call(t, cs, "list_projects", nil))
	if len(prj.Projects) != 1 || prj.Projects[0].Name != "cube" {
		t.Fatalf("%+v", prj)
	}
}
