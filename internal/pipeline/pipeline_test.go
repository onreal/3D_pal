package pipeline_test

import (
	"context"
	"errors"
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

var ctx = context.Background()

type recorder struct {
	mu     sync.Mutex
	events []string
}

func (r *recorder) Publish(typ string, _ any) {
	r.mu.Lock()
	r.events = append(r.events, typ)
	r.mu.Unlock()
}

func (r *recorder) has(typ string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.events {
		if e == typ {
			return true
		}
	}
	return false
}

type countingSlicer struct {
	fakeslicer.Slicer
	mu    sync.Mutex
	calls int
}

func (c *countingSlicer) Slice(ctx context.Context, in slicer.Input) (slicer.Result, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return c.Slicer.Slice(ctx, in)
}

type env struct {
	svc     *pipeline.Service
	printer *fake.Printer
	slicer  *countingSlicer
	events  *recorder
	cfg     *config.Store
	work    string
	runner  *runner.Fake
}

// newEnv wires a real store, a fake OpenSCAD that writes a cube sized by the "size"
// parameter, the fake slicer and the in-memory printer.
func newEnv(t *testing.T) *env {
	t.Helper()
	db := storetest.Open(t)
	work := t.TempDir()
	cfg := config.NewStore(db, filepath.Join(t.TempDir(), "profiles"))
	set := config.Defaults()
	set.Printers = []config.Printer{{ID: "sim", Name: "Simulated", Kind: "fake", Default: true, Config: map[string]string{"slicer": "fake"}}}
	if err := cfg.Save(set); err != nil {
		t.Fatal(err)
	}
	fr := &runner.Fake{OnRun: func(name string, args []string) ([]byte, error) {
		out := args[1] // -o <path>
		if strings.HasSuffix(out, ".png") {
			return nil, os.WriteFile(out, []byte("\x89PNG fake"), 0o644)
		}
		size := 20.0
		for i, a := range args {
			if a == "-D" && strings.HasPrefix(args[i+1], "size=") {
				var v float64
				_, _ = fmtSscan(strings.TrimPrefix(args[i+1], "size="), &v)
				size = v
			}
		}
		return nil, os.WriteFile(out, mesh.Cube(size, size, size), 0o644)
	}}
	p := fake.New(fake.Slicer("fake"))
	sl := &countingSlicer{}
	rec := &recorder{}
	svc := pipeline.New(pipeline.Deps{
		DB: db, Runner: fr, Config: cfg, Jobs: jobs.NewStore(db), Projects: project.NewStore(db, work),
		SCAD: scad.New(fr, "openscad"), Slicers: slicer.Set{"fake": sl}, Events: rec,
		NewPrinter: func(config.Printer) (printer.Printer, error) { return p, nil },
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		PollEvery:  10 * time.Millisecond,
	})
	t.Cleanup(svc.Close)
	return &env{svc: svc, printer: p, slicer: sl, events: rec, cfg: cfg, work: work, runner: fr}
}

const cube = "size = 20; // [5:1:300] Edge length in mm\ncube(size);\n"

func (e *env) mustRequestApproval(t *testing.T, proj string) pipeline.Approval {
	t.Helper()
	if _, err := e.svc.Compile(ctx, proj, cube, nil); err != nil {
		t.Fatal(err)
	}
	a, err := e.svc.RequestApproval(ctx, proj, 0, "", slicer.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestPrintNeverUploadsUnapprovedJob(t *testing.T) {
	e := newEnv(t)
	a := e.mustRequestApproval(t, "cube")

	_, err := e.svc.Print(ctx, a.Job.ID)

	if !errors.Is(err, jobs.ErrNotApproved) {
		t.Fatalf("want ErrNotApproved, got %v", err)
	}
	if e.printer.Uploads() != 0 || e.printer.Starts() != 0 {
		t.Fatal("printer was contacted for an unapproved job")
	}
}

func TestApprovedJobPrintsOnce(t *testing.T) {
	e := newEnv(t)
	a := e.mustRequestApproval(t, "cube")
	if _, err := e.svc.Approve(a.Job.ID); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.Print(ctx, a.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if e.printer.Uploads() != 1 || e.printer.Starts() != 1 || res.Remote != "cube_v1.gcode.3mf" {
		t.Fatalf("uploads=%d starts=%d remote=%s", e.printer.Uploads(), e.printer.Starts(), res.Remote)
	}
	if _, err := e.svc.Print(ctx, a.Job.ID); !errors.Is(err, jobs.ErrPrinted) {
		t.Fatalf("second print: want ErrPrinted, got %v", err)
	}
	if e.printer.Uploads() != 1 {
		t.Fatal("second print reached the printer")
	}
	for _, f := range []string{project.SCAD, project.STL, project.PNG, project.Job, project.Params, project.RunLog} {
		if _, err := os.Stat(filepath.Join(e.work, "cube", f)); err != nil {
			t.Errorf("project folder is missing %s", f)
		}
	}
}

func TestApprovalIsVoidWhenSliceChanges(t *testing.T) {
	e := newEnv(t)
	a := e.mustRequestApproval(t, "cube")
	e.svc.Approve(a.Job.ID)
	os.WriteFile(a.Job.FilePath, []byte("tampered"), 0o644)
	if _, err := e.svc.Print(ctx, a.Job.ID); !errors.Is(err, jobs.ErrFileChanged) {
		t.Fatalf("want ErrFileChanged, got %v", err)
	}
	if e.printer.Uploads() != 0 {
		t.Fatal("printer contacted")
	}
}

func TestCompileVersionsAndParams(t *testing.T) {
	e := newEnv(t)
	r1, err := e.svc.Compile(ctx, "cube", cube, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Version != 1 || r1.Size.X != 20 || !r1.Fits || len(r1.Images) != 2 {
		t.Fatalf("%+v", r1)
	}
	if !strings.Contains(r1.Summary(), "20.0 × 20.0 × 20.0 mm") || !strings.Contains(r1.Summary(), "size=20") {
		t.Fatalf("summary: %s", r1.Summary())
	}
	// Changing a parameter recompiles the same code with -D, as a new version.
	r2, err := e.svc.Compile(ctx, "cube", "", map[string]any{"size": 25.0})
	if err != nil {
		t.Fatal(err)
	}
	if r2.Version != 2 || r2.Size.X != 25 {
		t.Fatalf("%+v", r2)
	}
	params, _ := e.svc.Params("cube", 0)
	if len(params) != 1 || params[0].Value != 25.0 || *params[0].Max != 300 {
		t.Fatalf("%+v", params)
	}
	if !e.events.has("model") {
		t.Fatal("viewer was not told")
	}
}

func TestCompileErrorLeavesNoVersion(t *testing.T) {
	e := newEnv(t)
	e.runner.OnRun = func(string, []string) ([]byte, error) {
		return nil, errors.New("openscad failed: exit status 1\nERROR: Parser error in file \"model.scad\", line 1: syntax error")
	}
	if _, err := e.svc.Compile(ctx, "cube", "cube(;", nil); err == nil || !strings.Contains(err.Error(), "syntax error") {
		t.Fatalf("got %v", err)
	}
	if _, err := e.svc.Version("cube", 0); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("a failed compile left a version: %v", err)
	}
	runs, _ := e.svc.Runs("cube", 5)
	if len(runs) != 1 || runs[0].ExitCode == nil || *runs[0].ExitCode != 1 {
		t.Fatalf("run log: %+v", runs)
	}
}

func TestPartThatDoesNotFitIsRefused(t *testing.T) {
	e := newEnv(t)
	r, err := e.svc.Compile(ctx, "big", cube, map[string]any{"size": 300.0})
	if err != nil {
		t.Fatal(err)
	}
	if r.Fits || !strings.Contains(r.Summary(), "DOES NOT FIT") {
		t.Fatalf("%s", r.Summary())
	}
	if _, err := e.svc.RequestApproval(ctx, "big", 0, "", slicer.Overrides{}); !errors.Is(err, pipeline.ErrTooBig) {
		t.Fatalf("want ErrTooBig, got %v", err)
	}
}

func TestEstimatesAreCachedAndReusedAtApproval(t *testing.T) {
	e := newEnv(t)
	e.svc.Compile(ctx, "cube", cube, nil)
	e.svc.Compile(ctx, "cube", "", map[string]any{"size": 30.0})
	ests, err := e.svc.Estimate(ctx, "cube", []int{1, 2}, "", slicer.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ests) != 2 || ests[0].Grams >= ests[1].Grams {
		t.Fatalf("%+v", ests)
	}
	e.svc.Estimate(ctx, "cube", []int{1, 2}, "", slicer.Overrides{})
	if e.slicer.calls != 2 {
		t.Fatalf("sliced %d times, want 2 (cache)", e.slicer.calls)
	}
	if _, err := e.svc.RequestApproval(ctx, "cube", 2, "", slicer.Overrides{}); err != nil {
		t.Fatal(err)
	}
	if e.slicer.calls != 2 {
		t.Fatalf("approval sliced again (%d)", e.slicer.calls)
	}
	// A per-job override is a different slice.
	off := false
	e.svc.Estimate(ctx, "cube", []int{2}, "", slicer.Overrides{Supports: &off})
	if e.slicer.calls != 3 {
		t.Fatalf("override did not re-slice (%d)", e.slicer.calls)
	}
	vs, _ := e.svc.Versions("cube")
	if len(vs) != 2 || len(vs[1].Estimates) != 2 {
		t.Fatalf("%+v", vs)
	}
}

func TestEstimateNeverCreatesAJob(t *testing.T) {
	e := newEnv(t)
	e.svc.Compile(ctx, "cube", cube, nil)
	e.svc.Estimate(ctx, "cube", nil, "", slicer.Overrides{})
	if p, _ := e.svc.PendingJobs("cube"); len(p) != 0 {
		t.Fatalf("estimate created jobs: %+v", p)
	}
}

func TestStartEstimateRunsInBackground(t *testing.T) {
	e := newEnv(t)
	e.svc.Compile(ctx, "cube", cube, nil)
	est, err := e.svc.StartEstimate("cube", 1, "")
	if err != nil || est.Status != "running" {
		t.Fatalf("%+v %v", est, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		got, ok, _ := e.svc.CachedEstimate("cube", 1, "")
		if ok && got.Status == "done" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("estimate never finished")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestNoPrinterConfigured(t *testing.T) {
	e := newEnv(t)
	e.cfg.Save(config.Defaults())
	e.svc.Compile(ctx, "cube", cube, nil)
	if _, err := e.svc.RequestApproval(ctx, "cube", 0, "", slicer.Overrides{}); !errors.Is(err, config.ErrNoPrinter) {
		t.Fatalf("got %v", err)
	}
}

func TestPrinterStatusIsWatchedDuringAPrint(t *testing.T) {
	e := newEnv(t)
	a := e.mustRequestApproval(t, "cube")
	e.svc.Approve(a.Job.ID)
	if _, err := e.svc.Print(ctx, a.Job.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if !e.events.has("printer") {
		t.Fatal("no printer status events")
	}
	if err := e.svc.Cancel(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.svc.Status(ctx, ""); st.State != printer.Idle {
		t.Fatalf("after cancel: %+v", st)
	}
}

func TestBadProjectName(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.Compile(ctx, "../data", cube, nil); !errors.Is(err, project.ErrBadName) {
		t.Fatalf("got %v", err)
	}
}
