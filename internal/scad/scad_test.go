package scad_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/margarit/printpipe/internal/mesh"
	"github.com/margarit/printpipe/internal/runner"
	"github.com/margarit/printpipe/internal/scad"
)

func outPath(args []string) string {
	for i, a := range args {
		if a == "-o" {
			return args[i+1]
		}
	}
	return ""
}

func TestCompileArgsAndMesh(t *testing.T) {
	dir := t.TempDir()
	f := &runner.Fake{OnRun: func(name string, args []string) ([]byte, error) {
		return nil, os.WriteFile(outPath(args), mesh.Cube(20, 20, 20), 0o644)
	}}
	m, err := scad.New(f, "").Compile(context.Background(), filepath.Join(dir, "model.scad"), filepath.Join(dir, "model.stl"), []string{"-D", "size=20"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Size() != (mesh.Vec{X: 20, Y: 20, Z: 20}) {
		t.Fatalf("size %+v", m.Size())
	}
	got := strings.Join(f.Calls[0], " ")
	want := "openscad -o " + filepath.Join(dir, "model.stl") + " --export-format binstl -D size=20 " + filepath.Join(dir, "model.scad")
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestCompileErrorIsReturnedToTheAgent(t *testing.T) {
	f := &runner.Fake{OnRun: func(string, []string) ([]byte, error) {
		return nil, errors.New("openscad failed: exit status 1\nParsing design (AST generation)...\nERROR: Parser error in file \"model.scad\", line 3: syntax error")
	}}
	_, err := scad.New(f, "").Compile(context.Background(), "m.scad", filepath.Join(t.TempDir(), "m.stl"), nil)
	if err == nil || !strings.Contains(err.Error(), "line 3: syntax error") || strings.Contains(err.Error(), "Parsing design") {
		t.Fatalf("got %v", err)
	}
}

func TestEmptyModelIsAnError(t *testing.T) {
	f := &runner.Fake{OnRun: func(string, []string) ([]byte, error) {
		return []byte("WARNING: No top level geometry to render"), nil
	}}
	_, err := scad.New(f, "").Compile(context.Background(), "m.scad", filepath.Join(t.TempDir(), "m.stl"), nil)
	if err == nil || !strings.Contains(err.Error(), "no geometry") {
		t.Fatalf("got %v", err)
	}
}

func TestRenderUsesCameraAndCompiledSTL(t *testing.T) {
	dir := t.TempDir()
	var wrapper string
	f := &runner.Fake{OnRun: func(name string, args []string) ([]byte, error) {
		b, _ := os.ReadFile(args[len(args)-1])
		wrapper = string(b)
		return nil, nil
	}}
	if err := scad.New(f, "").Render(context.Background(), filepath.Join(dir, "model.stl"), filepath.Join(dir, "preview-iso.png"), "iso"); err != nil {
		t.Fatal(err)
	}
	line := strings.Join(f.Calls[0], " ")
	if !strings.Contains(line, "--camera=0,0,0,55,0,25,0") || !strings.Contains(line, "--viewall") {
		t.Fatalf("args: %s", line)
	}
	if !strings.Contains(wrapper, `import("model.stl"`) {
		t.Fatalf("wrapper: %s", wrapper)
	}
	if err := scad.New(f, "").Render(context.Background(), "m.stl", "p.png", "sideways"); err == nil {
		t.Fatal("unknown angle accepted")
	}
}

func TestManifoldBackendWhenAvailable(t *testing.T) {
	dir := t.TempDir()
	f := &runner.Fake{OnRun: func(name string, args []string) ([]byte, error) {
		if args[0] == "--backend=manifold" {
			return []byte("OpenSCAD version 2026.09.30"), nil
		}
		return nil, os.WriteFile(outPath(args), mesh.Cube(1, 1, 1), 0o644)
	}}
	c := scad.New(f, "")
	if got := c.UseBestBackend(context.Background()); got != "manifold" {
		t.Fatalf("backend %q", got)
	}
	c.Compile(context.Background(), "m.scad", filepath.Join(dir, "m.stl"), nil)
	if line := strings.Join(f.Calls[len(f.Calls)-1], " "); !strings.Contains(line, "--backend=manifold") {
		t.Fatalf("args: %s", line)
	}

	old := &runner.Fake{OnRun: func(name string, args []string) ([]byte, error) {
		if args[0] == "--backend=manifold" {
			return nil, errors.New("openscad failed: exit status 1\nunrecognised option '--backend=manifold'")
		}
		return nil, os.WriteFile(outPath(args), mesh.Cube(1, 1, 1), 0o644)
	}}
	c2 := scad.New(old, "")
	if got := c2.UseBestBackend(context.Background()); got != "" {
		t.Fatalf("2021 got backend %q", got)
	}
	c2.Compile(context.Background(), "m.scad", filepath.Join(dir, "m.stl"), nil)
	if line := strings.Join(old.Calls[len(old.Calls)-1], " "); strings.Contains(line, "--backend") {
		t.Fatalf("args: %s", line)
	}
}

func TestCompileReturnsEchoAndWarnings(t *testing.T) {
	dir := t.TempDir()
	f := &runner.Fake{OnRun: func(name string, args []string) ([]byte, error) {
		out := "Parsing design (AST generation)...\nECHO: \"gap 0.2 mm\"\nECHO: \"gap 0.2 mm\"\nECHO: \"ratio\", 0.714\nWARNING: Ignoring unknown variable \"x\" in file model.scad, line 3\nTotal rendering time: 0:00:01\n"
		return []byte(out), os.WriteFile(outPath(args), mesh.Cube(1, 1, 1), 0o644)
	}}
	_, msgs, err := scad.New(f, "").CompileLog(context.Background(), "m.scad", filepath.Join(dir, "m.stl"), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"gap 0.2 mm", `"ratio", 0.714`, `WARNING: Ignoring unknown variable "x" in file model.scad, line 3`}
	if strings.Join(msgs, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", msgs)
	}
}

func TestMessagesAreCapped(t *testing.T) {
	var b strings.Builder
	for i := 0; i < scad.MaxMessages+10; i++ {
		b.WriteString("ECHO: " + strings.Repeat("x", i+1) + "\n")
	}
	msgs := scad.Messages([]byte(b.String()))
	if len(msgs) != scad.MaxMessages+1 || msgs[len(msgs)-1] != "(more messages cut)" {
		t.Fatalf("%d messages, last %q", len(msgs), msgs[len(msgs)-1])
	}
}
