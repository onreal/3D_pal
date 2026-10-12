package mcpserver_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/margarit/printpipe/internal/mcpserver"
	"github.com/margarit/printpipe/internal/pipeline/pipelinetest"
	"github.com/margarit/printpipe/internal/sessions"
)

func TestStdioSessionIsTracked(t *testing.T) {
	env := pipelinetest.New(t)
	reg := sessions.NewRegistry()
	server := mcpserver.New(env.Svc, "test", reg)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Title: "Claude Code", Version: "2.1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	wait := func(cond func() bool) {
		deadline := time.Now().Add(5 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatal("timed out")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	wait(func() bool { return reg.OpenCount() == 1 })
	call(t, cs, "compile_model", map[string]any{"project": "cube", "scad_code": pipelinetest.Cube})
	call(t, cs, "list_printers", nil)
	list := reg.List()
	s := list[0]
	if !strings.HasPrefix(s.ID, "stdio-") || s.Transport != sessions.Stdio || s.Client != "claude-code" || s.Title != "Claude Code" ||
		s.Calls != 2 || s.Project != "cube" || s.LastTool != "list_printers" {
		t.Fatalf("%+v", s)
	}
	runs, _ := env.Svc.Runs(pipelineFilter(s.ID), 10)
	if len(runs) != 1 || runs[0].Client != "claude-code" {
		t.Fatalf("runs %+v", runs)
	}
	if !reg.End(s.ID) {
		t.Fatal("End failed")
	}
	ss.Wait()
	cs.Close()
	wait(func() bool { return reg.OpenCount() == 0 })
	_ = context.Background
}
