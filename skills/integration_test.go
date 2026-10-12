//go:build integration

package skills_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/margarit/printpipe/internal/runner"
	"github.com/margarit/printpipe/internal/scad"
	"github.com/margarit/printpipe/skills"
)

// The templates are what agents copy, so every option must compile cleanly with the
// image's real OpenSCAD: docker build --target integration .
func TestTemplatesCompileWithRealOpenSCAD(t *testing.T) {
	c := scad.New(runner.Exec{}, os.Getenv("OPENSCAD"))
	ctx := context.Background()
	c.UseBestBackend(ctx)
	for _, name := range []string{"single-part-template", "multi-part-template"} {
		code := skills.Text(name)
		dir := t.TempDir()
		path := filepath.Join(dir, "model.scad")
		os.WriteFile(path, []byte(code), 0o644)
		decl := scad.ParseParams(code)
		var options []string
		for _, p := range decl {
			if p.Name == "part" {
				for _, o := range p.Options {
					options = append(options, fmt.Sprint(o.Value))
				}
			}
		}
		if len(options) == 0 {
			t.Fatalf("%s: no part options found", name)
		}
		for _, opt := range options {
			defines, err := scad.Defines(decl, map[string]any{"part": opt})
			if err != nil {
				t.Fatal(err)
			}
			m, msgs, err := c.CompileLog(ctx, path, filepath.Join(dir, opt+".stl"), defines)
			if err != nil {
				t.Errorf("%s %s: %v", name, opt, err)
				continue
			}
			for _, msg := range msgs {
				if !strings.HasPrefix(msg, "INFO:") {
					t.Errorf("%s %s: unexpected output %q", name, opt, msg)
				}
			}
			lo, _ := m.Bounds()
			s := m.Size()
			switch {
			case strings.HasPrefix(opt, "check_"):
				if s.X > 2.01 || s.Y > 2.01 || s.Z > 2.01 {
					t.Errorf("%s %s: clash, size %+v (clean is the 2 mm marker)", name, opt, s)
				}
			case lo.Z < -0.001 || lo.Z > 0.001:
				t.Errorf("%s %s: lowest point at z %.3f, want 0 (flat on the bed)", name, opt, lo.Z)
			case s.X > 250 || s.Y > 250 || s.Z > 250:
				t.Errorf("%s %s: %+v does not fit the bed", name, opt, s)
			}
		}
	}
}

// A check that can't fail proves nothing: with a negative clearance the multi-part
// template's interference check must report a clash.
func TestInterferenceCheckCatchesAClash(t *testing.T) {
	c := scad.New(runner.Exec{}, os.Getenv("OPENSCAD"))
	ctx := context.Background()
	c.UseBestBackend(ctx)
	code := skills.Text("multi-part-template")
	dir := t.TempDir()
	path := filepath.Join(dir, "model.scad")
	os.WriteFile(path, []byte(code), 0o644)
	defines, err := scad.Defines(scad.ParseParams(code), map[string]any{"part": "check_interference", "lid_clearance": -0.1})
	if err != nil {
		t.Fatal(err)
	}
	m, err := c.Compile(ctx, path, filepath.Join(dir, "clash.stl"), defines)
	if err != nil {
		t.Fatal(err)
	}
	if s := m.Size(); s.X < 10 {
		t.Fatalf("no clash found with lid_clearance -0.1: size %+v", s)
	}
}
