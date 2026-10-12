package project_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/margarit/printpipe/internal/project"
	"github.com/margarit/printpipe/internal/store/storetest"
)

func TestNames(t *testing.T) {
	for _, ok := range []string{"cube", "test-cube", "Bracket_2"} {
		if project.ValidName(ok) != nil {
			t.Errorf("%q rejected", ok)
		}
	}
	for _, bad := range []string{"", "../data", "a/b", "-x", ".hidden", "with space"} {
		if !errors.Is(project.ValidName(bad), project.ErrBadName) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func compile(t *testing.T, s *project.Store, name string, params map[string]any) project.Version {
	t.Helper()
	v, err := s.Next(name)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{project.SCAD, project.STL, project.PNG} {
		os.WriteFile(v.Path(f), []byte(f+" of v"+string(rune('0'+v.Number))), 0o644)
	}
	v.Params, v.STLHash = params, "hash"
	v, err = s.Commit(v)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestVersionsAreNumberedAndCurrentFilesCopied(t *testing.T) {
	work := t.TempDir()
	s := project.NewStore(storetest.Open(t), work)
	compile(t, s, "cube", map[string]any{"size": 20.0})
	v2 := compile(t, s, "cube", map[string]any{"size": 25.0})
	if v2.Number != 2 || v2.Dir != filepath.Join(work, "cube", "v2") {
		t.Fatalf("%+v", v2)
	}
	cur, _ := os.ReadFile(filepath.Join(work, "cube", project.STL))
	if string(cur) != "model.stl of v2" {
		t.Fatalf("current STL is %q", cur)
	}
	latest, err := s.Get("cube", 0)
	if err != nil || latest.Number != 2 || latest.Params["size"] != 25.0 {
		t.Fatalf("%+v %v", latest, err)
	}
	all, _ := s.List("cube")
	if len(all) != 2 || all[0].Number != 1 {
		t.Fatalf("%+v", all)
	}
	ps, _ := s.Projects()
	if len(ps) != 1 || ps[0].Latest != 2 {
		t.Fatalf("%+v", ps)
	}
}

func TestFailedCompileLeavesNoVersion(t *testing.T) {
	s := project.NewStore(storetest.Open(t), t.TempDir())
	v, _ := s.Next("cube")
	s.Discard(v)
	if _, err := s.Get("cube", 0); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if v2 := compile(t, s, "cube", nil); v2.Number != 1 {
		t.Fatalf("got v%d", v2.Number)
	}
}

func TestUnknownVersion(t *testing.T) {
	s := project.NewStore(storetest.Open(t), t.TempDir())
	compile(t, s, "cube", nil)
	if _, err := s.Get("cube", 9); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestPromoteAndLog(t *testing.T) {
	work := t.TempDir()
	s := project.NewStore(storetest.Open(t), work)
	v := compile(t, s, "cube", nil)
	job := filepath.Join(v.Dir, "slices", "job.gcode.3mf")
	os.MkdirAll(filepath.Dir(job), 0o755)
	os.WriteFile(job, []byte("sliced"), 0o644)
	if err := s.Promote(v, job, project.Job); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(work, "cube", project.Job)); string(b) != "sliced" {
		t.Fatalf("%q", b)
	}
	s.Log("cube", "compile_model exit=0\n")
	s.Log("../evil", "ignored")
	if b, _ := os.ReadFile(filepath.Join(work, "cube", project.RunLog)); !strings.Contains(string(b), "compile_model exit=0") {
		t.Fatalf("%q", b)
	}
	if s.Work() != work || v.Path(project.STL) != filepath.Join(work, "cube", "v1", "model.stl") {
		t.Fatal("paths")
	}
	if err := s.Promote(v, filepath.Join(work, "missing"), project.Job); err == nil {
		t.Fatal("missing source promoted")
	}
	if _, err := s.List("../x"); err == nil {
		t.Fatal("bad name listed")
	}
	if _, err := s.Get("../x", 0); err == nil {
		t.Fatal("bad name read")
	}
}
