package assembly_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/margarit/printpipe/internal/assembly"
	"github.com/margarit/printpipe/internal/mesh"
	"github.com/margarit/printpipe/internal/project"
	"github.com/margarit/printpipe/internal/store/storetest"
)

// fakeSCAD draws boxes for each assembly step, or all of them when no step is selected.
type fakeSCAD struct {
	mu    sync.Mutex
	calls [][]string
	boxes map[string][]mesh.Box // step ID → boxes; "" → the whole assembly
}

func (f *fakeSCAD) Export3MF(_ context.Context, _ string, out string, defines []string) error {
	f.mu.Lock()
	f.calls = append(f.calls, defines)
	f.mu.Unlock()
	step := ""
	for i, d := range defines {
		if d == "-D" && strings.HasPrefix(defines[i+1], "assembly_only=") {
			step = strings.Trim(strings.TrimPrefix(defines[i+1], "assembly_only="), `"`)
		}
	}
	b, ok := f.boxes[step]
	if !ok || len(b) == 0 {
		return mesh.Write3MF(out, nil) // renders nothing
	}
	return mesh.Write3MF(out, b)
}

func box(x0, y0, z0, x1, y1, z1 float64, color string) mesh.Box {
	return mesh.Box{Min: mesh.Vec{X: x0, Y: y0, Z: z0}, Max: mesh.Vec{X: x1, Y: y1, Z: z1}, Color: color}
}

func version(t *testing.T, code string, params map[string]any) project.Version {
	t.Helper()
	ps := project.NewStore(storetest.Open(t), t.TempDir())
	v, err := ps.Next("gear")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(v.Path(project.SCAD), []byte(code), 0o644)
	os.WriteFile(v.Path(project.STL), mesh.Cube(1, 1, 1), 0o644)
	v.Params = params
	v, err = ps.Commit(v)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

const stepped = `part = "plate"; // [plate, assembled, ring]
size = 20; // [5:50]
/* [Hidden] */
assembly = [["base", "Base plate"], ["pins", "Pins"], ["lid", "Lid"]];
assembly_only = "";
`

func TestDeclaredStepsRenderOneByOne(t *testing.T) {
	f := &fakeSCAD{boxes: map[string][]mesh.Box{
		"base": {box(0, 0, 0, 20, 20, 2, "#FF720C")},
		"pins": {box(2, 2, 2, 4, 4, 8, "#28282B"), box(16, 16, 2, 18, 18, 8, "#28282B")},
		"lid":  {box(0, 0, 8, 20, 20, 10, "#FF720C")},
	}}
	v := version(t, stepped, map[string]any{"part": "plate", "size": 25.0})
	s := assembly.New(f, nil, nil)
	r := s.Build(context.Background(), v)
	if r.Status != "done" || r.Mode != assembly.Steps || len(r.Parts) != 3 {
		t.Fatalf("%+v", r)
	}
	if p := r.Parts[1]; p.Label != "Pins" || p.Count != 2 || p.Color != "#28282B" || p.Min.Z != 2 || p.Max.Z != 8 {
		t.Fatalf("pins: %+v", p)
	}
	if r.Max.Z != 10 || r.Parts[0].Label != "Base plate" {
		t.Fatalf("%+v", r)
	}
	// Every step was rendered assembled (part override) with the version's values.
	for _, defs := range f.calls {
		line := strings.Join(defs, " ")
		if !strings.Contains(line, `part="assembled"`) || !strings.Contains(line, "size=25") {
			t.Fatalf("defines: %s", line)
		}
	}
	if _, err := os.Stat(assembly.PartFile(v, 2)); err != nil {
		t.Fatal("lid STL not written")
	}
	if cached, ok := s.Cached(v); !ok || len(cached.Parts) != 3 {
		t.Fatal("not cached")
	}
}

func TestEmptyStepIsReported(t *testing.T) {
	f := &fakeSCAD{boxes: map[string][]mesh.Box{"base": {box(0, 0, 0, 1, 1, 1, "#FFFFFF")}}}
	v := version(t, stepped, nil)
	r := assembly.New(f, nil, nil).Build(context.Background(), v)
	if r.Status != "done" || r.Parts[1].Error == "" || !strings.Contains(r.Parts[1].Error, "assembly_only") {
		t.Fatalf("%+v", r.Parts)
	}
}

func TestPiecesWhenNoStepsAreDeclared(t *testing.T) {
	f := &fakeSCAD{boxes: map[string][]mesh.Box{"": {
		box(0, 0, 5, 20, 20, 7, "#FF720C"), // lid (top)
		box(2, 2, 2, 4, 4, 4, "#28282B"),   // two identical pins
		box(10, 10, 2, 12, 12, 4, "#28282B"),
		box(0, 0, 0, 20, 20, 1, "#FF720C"), // base (bottom)
	}}}
	v := version(t, "size = 1;\ncube(size);\n", nil)
	r := assembly.New(f, nil, nil).Build(context.Background(), v)
	if r.Status != "done" || r.Mode != assembly.Pieces || r.Note == "" || len(r.Parts) != 3 {
		t.Fatalf("%+v", r)
	}
	if r.Parts[0].Min.Z != 0 || r.Parts[1].Count != 2 || r.Parts[1].Label != "Part 2" || r.Parts[2].Min.Z != 5 {
		t.Fatalf("bottom to top, instances grouped: %+v", r.Parts)
	}
}

func TestStartBuildsInBackgroundOnce(t *testing.T) {
	f := &fakeSCAD{boxes: map[string][]mesh.Box{"": {box(0, 0, 0, 1, 1, 1, "#FFFFFF")}}}
	v := version(t, "x = 1;\ncube(x);\n", nil)
	var published []assembly.Result
	var mu sync.Mutex
	s := assembly.New(f, nil, func(r assembly.Result) { mu.Lock(); published = append(published, r); mu.Unlock() })
	if r := s.Start(v); r.Status != "running" {
		t.Fatalf("%+v", r)
	}
	s.Start(v)
	s.Wait(v)
	if r := s.Start(v); r.Status != "done" {
		t.Fatalf("%+v", r)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(published) != 1 || len(f.calls) != 1 {
		t.Fatalf("published %d, rendered %d", len(published), len(f.calls))
	}
}

type failingSCAD struct{}

func (failingSCAD) Export3MF(context.Context, string, string, []string) error {
	return errors.New("OpenSCAD could not render the assembly")
}

func TestFailures(t *testing.T) {
	v := version(t, "x = 1;\ncube(x);\n", nil)
	if r := assembly.New(failingSCAD{}, nil, nil).Build(context.Background(), v); r.Status != "failed" || !strings.Contains(r.Error, "could not render") {
		t.Fatalf("%+v", r)
	}
	bad := version(t, "assembly = [\"a\"];\n", nil) // no assembly_only
	if r := assembly.New(&fakeSCAD{}, nil, nil).Build(context.Background(), bad); r.Status != "failed" || !strings.Contains(r.Error, "assembly_only") {
		t.Fatalf("%+v", r)
	}
	none := version(t, stepped, nil) // every step empty
	if r := assembly.New(&fakeSCAD{boxes: map[string][]mesh.Box{}}, nil, nil).Build(context.Background(), none); r.Status != "failed" {
		t.Fatalf("%+v", r)
	}
}
