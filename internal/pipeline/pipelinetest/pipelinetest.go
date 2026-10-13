// Package pipelinetest wires a real pipeline to fakes: OpenSCAD writes a cube sized by
// the "size" parameter, the fake slicer slices it and the in-memory printer prints it.
package pipelinetest

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/margarit/printpipe/internal/config"
	"github.com/margarit/printpipe/internal/jobs"
	"github.com/margarit/printpipe/internal/mesh"
	"github.com/margarit/printpipe/internal/pipeline"
	"github.com/margarit/printpipe/internal/printer"
	"github.com/margarit/printpipe/internal/printers/fake"
	"github.com/margarit/printpipe/internal/project"
	"github.com/margarit/printpipe/internal/runner"
	"github.com/margarit/printpipe/internal/scad"
	"github.com/margarit/printpipe/internal/slicer"
	fakeslicer "github.com/margarit/printpipe/internal/slicers/fake"
	"github.com/margarit/printpipe/internal/store/storetest"
)

// Cube is a one-parameter model the fake OpenSCAD understands.
const Cube = "size = 20; // [5:1:300] Edge length in mm\ncube(size);\n"

// Recorder keeps published event types.
type Recorder struct {
	mu     sync.Mutex
	Events []string
	Next   func(typ string, v any) // optional pass-through
}

func (r *Recorder) Publish(typ string, v any) {
	r.mu.Lock()
	r.Events = append(r.Events, typ)
	next := r.Next
	r.mu.Unlock()
	if next != nil {
		next(typ, v)
	}
}

func (r *Recorder) Has(typ string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.Events {
		if e == typ {
			return true
		}
	}
	return false
}

// CountingSlicer is the fake slicer, counting slices. Shipped stands in for the presets a
// real slicer ships (Bambu Studio's system presets).
type CountingSlicer struct {
	fakeslicer.Slicer
	Shipped []slicer.Preset
	mu      sync.Mutex
	calls   int
	last    slicer.Input
}

func (c *CountingSlicer) Slice(ctx context.Context, in slicer.Input) (slicer.Result, error) {
	c.mu.Lock()
	c.calls++
	c.last = in
	c.mu.Unlock()
	return c.Slicer.Slice(ctx, in)
}

func (c *CountingSlicer) Presets(model string) []slicer.Preset { return c.Shipped }

// Last is the input of the latest slice.
func (c *CountingSlicer) Last() slicer.Input { c.mu.Lock(); defer c.mu.Unlock(); return c.last }

func (c *CountingSlicer) Calls() int { c.mu.Lock(); defer c.mu.Unlock(); return c.calls }

type Env struct {
	Svc      *pipeline.Service
	SCAD     *scad.Compiler
	Projects *project.Store
	Printer  *fake.Printer
	Slicer   *CountingSlicer
	Events   *Recorder
	Config   *config.Store
	Jobs     *jobs.Store
	Work     string
	Runner   *runner.Fake
}

// FakeOpenSCAD writes a cube STL (or a placeholder PNG) for the -o path in args.
func FakeOpenSCAD(name string, args []string) ([]byte, error) {
	out := args[1] // -o <path>
	if strings.HasSuffix(out, ".png") {
		return nil, os.WriteFile(out, []byte("\x89PNG\r\n\x1a\nfake"), 0o644)
	}
	size := 20.0
	for i, a := range args {
		if a == "-D" && strings.HasPrefix(args[i+1], "size=") {
			fmt.Sscan(strings.TrimPrefix(args[i+1], "size="), &size)
		}
	}
	if strings.HasSuffix(out, ".3mf") { // assembly: a base and a lid with a gap between
		return nil, mesh.Write3MF(out, []mesh.Box{
			{Max: mesh.Vec{X: size, Y: size, Z: 2}, Color: "#FF720C"},
			{Min: mesh.Vec{Z: size - 2}, Max: mesh.Vec{X: size, Y: size, Z: size}, Color: "#28282B"},
		})
	}
	return nil, os.WriteFile(out, mesh.Cube(size, size, size), 0o644)
}

func New(t testing.TB) *Env {
	t.Helper()
	db := storetest.Open(t)
	work := t.TempDir()
	cfg := config.NewStore(db, filepath.Join(t.TempDir(), "profiles"))
	set := config.Defaults()
	set.Printers = []config.Printer{{ID: "sim", Name: "Simulated", Kind: "fake", Default: true, Config: map[string]string{"slicer": "fake"}}}
	if err := cfg.Save(set); err != nil {
		t.Fatal(err)
	}
	fr := &runner.Fake{OnRun: FakeOpenSCAD}
	p := fake.New(fake.Slicer("fake"))
	sl := &CountingSlicer{}
	rec := &Recorder{}
	js := jobs.NewStore(db)
	projects := project.NewStore(db, work)
	scadC := scad.New(fr, "openscad")
	svc := pipeline.New(pipeline.Deps{
		DB: db, Runner: fr, Config: cfg, Jobs: js, Projects: projects,
		SCAD: scadC, Slicers: slicer.Set{"fake": sl}, Events: rec,
		NewPrinter: func(config.Printer) (printer.Printer, error) { return p, nil },
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		PollEvery:  10 * time.Millisecond,
	})
	t.Cleanup(svc.Close)
	return &Env{Svc: svc, SCAD: scadC, Projects: projects, Printer: p, Slicer: sl, Events: rec, Config: cfg, Jobs: js, Work: work, Runner: fr}
}
