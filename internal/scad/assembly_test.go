package scad_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/margarit/printpipe/internal/mesh"
	"github.com/margarit/printpipe/internal/runner"
	"github.com/margarit/printpipe/internal/scad"
)

func TestParseAssembly(t *testing.T) {
	src := `part = "assembled"; // [assembled, ring, sun]
/* [Hidden] */
assembly = [
  ["cap_bottom", "Bottom cap"],
  ["ring", "Ring, with \"teeth\""],   // a comment ] with a bracket
  "sun",
  "sun",
];
assembly_only = "";
`
	steps, err := scad.ParseAssembly(src)
	if err != nil {
		t.Fatal(err)
	}
	want := []scad.Step{{ID: "cap_bottom", Label: "Bottom cap"}, {ID: "ring", Label: `Ring, with "teeth"`}, {ID: "sun", Label: "sun"}}
	if len(steps) != 3 || steps[0] != want[0] || steps[1] != want[1] || steps[2] != want[2] {
		t.Fatalf("%+v", steps)
	}
	if s, err := scad.ParseAssembly("cube(1);"); s != nil || err != nil {
		t.Fatal("a model without steps", s, err)
	}
	if _, err := scad.ParseAssembly(`assembly = ["a"];`); err == nil || !strings.Contains(err.Error(), "assembly_only") {
		t.Fatalf("missing assembly_only: %v", err)
	}
	if _, err := scad.ParseAssembly("assembly = [];\nassembly_only = \"\";"); err == nil {
		t.Fatal("empty list accepted")
	}
	if _, err := scad.ParseAssembly("assembly = [\"a\";\nassembly_only = \"\";"); err == nil {
		t.Fatal("unclosed list accepted")
	}
}

func TestAssembledOption(t *testing.T) {
	params := scad.ParseParams(`part = "plate"; // [plate, Assembled, sun]` + "\n")
	if v, ok := scad.AssembledOption(params); !ok || v != "Assembled" {
		t.Fatal(v, ok)
	}
	if _, ok := scad.AssembledOption(scad.ParseParams("size = 2;\n")); ok {
		t.Fatal("found an assembled option in a model without one")
	}
}

func TestExport3MF(t *testing.T) {
	dir := t.TempDir()
	f := &runner.Fake{OnRun: func(name string, args []string) ([]byte, error) {
		return nil, mesh.Write3MF(outPath(args), []mesh.Box{{Max: mesh.Vec{X: 1, Y: 1, Z: 1}, Color: "#112233"}})
	}}
	c := scad.New(f, "")
	out := filepath.Join(dir, "a.3mf")
	if err := c.Export3MF(context.Background(), "m.scad", out, []string{"-D", `assembly_only="ring"`}); err != nil {
		t.Fatal(err)
	}
	if line := strings.Join(f.Calls[0], " "); !strings.Contains(line, `-D assembly_only="ring"`) || !strings.HasSuffix(line, "m.scad") {
		t.Fatal(line)
	}
	bad := &runner.Fake{OnRun: func(string, []string) ([]byte, error) { return nil, nil }}
	if err := scad.New(bad, "").Export3MF(context.Background(), "m.scad", filepath.Join(dir, "none.3mf"), nil); err == nil {
		t.Fatal("no output accepted")
	}
}
