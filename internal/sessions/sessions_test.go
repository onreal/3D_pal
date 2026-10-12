package sessions_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/margarit/printpipe/internal/sessions"
)

func TestLifecycle(t *testing.T) {
	now := time.Unix(1000, 0)
	r := sessions.NewRegistry().WithClock(func() time.Time { return now })
	var events []string
	r.OnChange = func(kind string, i sessions.Info) { events = append(events, kind+":"+i.ID) }

	r.Transport("a", sessions.HTTP, "127.0.0.1:5000", "claude-code/2.1") // HTTP details arrive first
	closed := false
	r.Open("a", "claude-code", "2.1.0", "Claude Code", sessions.HTTP, func() { closed = true })
	r.Open("b", "", "", "", sessions.Stdio, nil)
	now = now.Add(time.Minute)
	r.Touch("b", "compile_model", "cube")
	r.Touch("missing", "x", "y")

	a, _ := r.Get("a")
	if a.RemoteAddr != "127.0.0.1:5000" || a.UserAgent != "claude-code/2.1" || a.Client != "claude-code" {
		t.Fatalf("%+v", a)
	}
	b, _ := r.Get("b")
	if b.Client != "unknown client" || b.Calls != 1 || b.Project != "cube" || b.LastTool != "compile_model" {
		t.Fatalf("%+v", b)
	}
	if list := r.List(); list[0].ID != "b" || r.OpenCount() != 2 {
		t.Fatalf("most recently active first: %+v", list)
	}
	if !r.End("a") || !closed {
		t.Fatal("End did not close the session")
	}
	if r.End("a") || r.End("b") {
		t.Fatal("End of a closed or close-less session reported success")
	}
	now = now.Add(time.Minute)
	r.Close("b")
	list := r.List()
	if r.OpenCount() != 0 || len(list) != 2 || list[0].ID != "b" || list[0].Open {
		t.Fatalf("%+v", list)
	}
	want := "opened:a opened:b updated:b closed:a closed:b"
	if got := fmt.Sprint(events); got != "["+want+"]" {
		t.Fatalf("events %s", got)
	}
}

func TestClosedSessionsArePruned(t *testing.T) {
	now := time.Unix(0, 0)
	r := sessions.NewRegistry().WithClock(func() time.Time { now = now.Add(time.Second); return now })
	for i := 0; i < 30; i++ {
		id := fmt.Sprint(i)
		r.Open(id, "c", "1", "", sessions.HTTP, nil)
		r.Close(id)
	}
	if n := len(r.List()); n != 20 {
		t.Fatalf("kept %d closed sessions, want 20", n)
	}
	if _, ok := r.Get("29"); !ok {
		t.Fatal("newest closed session pruned")
	}
}

func TestCallerInContext(t *testing.T) {
	ctx := sessions.WithCaller(context.Background(), sessions.Caller{ID: "s1", Client: "claude-code"})
	if c, ok := sessions.From(ctx); !ok || c.ID != "s1" {
		t.Fatal(c, ok)
	}
	if _, ok := sessions.From(context.Background()); ok {
		t.Fatal("caller in an empty context")
	}
}
