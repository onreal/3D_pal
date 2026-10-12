//go:build integration

package scad_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/margarit/printpipe/internal/runner"
	"github.com/margarit/printpipe/internal/scad"
)

// Runs the real OpenSCAD from the image: docker build --target integration .
func TestRealOpenSCADCompileAndRender(t *testing.T) {
	dir := t.TempDir()
	src, err := os.ReadFile("../../testdata/cube.scad")
	if err != nil {
		t.Fatal(err)
	}
	scadPath := filepath.Join(dir, "model.scad")
	os.WriteFile(scadPath, src, 0o644)
	c := scad.New(runner.Exec{}, os.Getenv("OPENSCAD"))
	ctx := context.Background()
	if b := c.UseBestBackend(ctx); b != "manifold" {
		t.Fatalf("the image's OpenSCAD should offer the manifold backend, got %q", b)
	}

	defines, err := scad.Defines(scad.ParseParams(string(src)), map[string]any{"size": 25.0})
	if err != nil {
		t.Fatal(err)
	}
	m, err := c.Compile(ctx, scadPath, filepath.Join(dir, "model.stl"), defines)
	if err != nil {
		t.Fatal(err)
	}
	if s := m.Size(); s.X < 24.99 || s.X > 25.01 || s.Z < 24.99 || s.Z > 25.01 {
		t.Fatalf("size %+v, want 25 mm", s)
	}

	for angle := range scad.Cameras {
		png := filepath.Join(dir, "preview-"+angle+".png")
		if err := c.Render(ctx, filepath.Join(dir, "model.stl"), png, angle); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(png)
		if err != nil || len(b) < 1000 || !bytes.HasPrefix(b, []byte("\x89PNG")) {
			t.Fatalf("%s preview: %d bytes, %v", angle, len(b), err)
		}
	}
}

func TestRealOpenSCADReportsSyntaxErrors(t *testing.T) {
	dir := t.TempDir()
	scadPath := filepath.Join(dir, "model.scad")
	os.WriteFile(scadPath, []byte("size = 20;\ncube(size;\n"), 0o644)
	_, err := scad.New(runner.Exec{}, os.Getenv("OPENSCAD")).Compile(context.Background(), scadPath, filepath.Join(dir, "model.stl"), nil)
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("line 2")) {
		t.Fatalf("want a parser error on line 2, got %v", err)
	}
}

func TestRealOpenSCADEmptyModel(t *testing.T) {
	dir := t.TempDir()
	scadPath := filepath.Join(dir, "model.scad")
	os.WriteFile(scadPath, []byte("size = 20;\nmodule box() { cube(size); }\n"), 0o644)
	_, err := scad.New(runner.Exec{}, os.Getenv("OPENSCAD")).Compile(context.Background(), scadPath, filepath.Join(dir, "model.stl"), nil)
	if err == nil {
		t.Fatal("an empty model compiled")
	}
	t.Log(err)
}
