package pipeline_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/margarit/printpipe/internal/config"
	"github.com/margarit/printpipe/internal/jobs"
	"github.com/margarit/printpipe/internal/pipeline"
	"github.com/margarit/printpipe/internal/pipeline/pipelinetest"
	"github.com/margarit/printpipe/internal/printer"
	"github.com/margarit/printpipe/internal/project"
	"github.com/margarit/printpipe/internal/slicer"
)

var ctx = context.Background()

type env = pipelinetest.Env

func newEnv(t *testing.T) *env { return pipelinetest.New(t) }

const cube = pipelinetest.Cube

func mustRequestApproval(t *testing.T, e *env, proj string) pipeline.Approval {
	t.Helper()
	if _, err := e.Svc.Compile(ctx, proj, cube, nil); err != nil {
		t.Fatal(err)
	}
	a, err := e.Svc.RequestApproval(ctx, proj, 0, "", slicer.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func TestPrintNeverUploadsUnapprovedJob(t *testing.T) {
	e := newEnv(t)
	a := mustRequestApproval(t, e, "cube")

	_, err := e.Svc.Print(ctx, a.Job.ID)

	if !errors.Is(err, jobs.ErrNotApproved) {
		t.Fatalf("want ErrNotApproved, got %v", err)
	}
	if e.Printer.Uploads() != 0 || e.Printer.Starts() != 0 {
		t.Fatal("printer was contacted for an unapproved job")
	}
}

func TestApprovedJobPrintsOnce(t *testing.T) {
	e := newEnv(t)
	a := mustRequestApproval(t, e, "cube")
	if _, err := e.Svc.Approve(a.Job.ID); err != nil {
		t.Fatal(err)
	}
	res, err := e.Svc.Print(ctx, a.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if e.Printer.Uploads() != 1 || e.Printer.Starts() != 1 || res.Remote != "cube_v1.gcode.3mf" {
		t.Fatalf("uploads=%d starts=%d remote=%s", e.Printer.Uploads(), e.Printer.Starts(), res.Remote)
	}
	if _, err := e.Svc.Print(ctx, a.Job.ID); !errors.Is(err, jobs.ErrPrinted) {
		t.Fatalf("second print: want ErrPrinted, got %v", err)
	}
	if e.Printer.Uploads() != 1 {
		t.Fatal("second print reached the printer")
	}
	for _, f := range []string{project.SCAD, project.STL, project.PNG, project.Job, project.Params, project.RunLog} {
		if _, err := os.Stat(filepath.Join(e.Work, "cube", f)); err != nil {
			t.Errorf("project folder is missing %s", f)
		}
	}
}

func TestApprovalIsVoidWhenSliceChanges(t *testing.T) {
	e := newEnv(t)
	a := mustRequestApproval(t, e, "cube")
	e.Svc.Approve(a.Job.ID)
	os.WriteFile(a.Job.FilePath, []byte("tampered"), 0o644)
	if _, err := e.Svc.Print(ctx, a.Job.ID); !errors.Is(err, jobs.ErrFileChanged) {
		t.Fatalf("want ErrFileChanged, got %v", err)
	}
	if e.Printer.Uploads() != 0 {
		t.Fatal("printer contacted")
	}
}

func TestCompileVersionsAndParams(t *testing.T) {
	e := newEnv(t)
	r1, err := e.Svc.Compile(ctx, "cube", cube, nil)
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
	r2, err := e.Svc.Compile(ctx, "cube", "", map[string]any{"size": 25.0})
	if err != nil {
		t.Fatal(err)
	}
	if r2.Version != 2 || r2.Size.X != 25 {
		t.Fatalf("%+v", r2)
	}
	params, _ := e.Svc.Params("cube", 0)
	if len(params) != 1 || params[0].Value != 25.0 || *params[0].Max != 300 {
		t.Fatalf("%+v", params)
	}
	if !e.Events.Has("model") {
		t.Fatal("viewer was not told")
	}
}

func TestCompileErrorLeavesNoVersion(t *testing.T) {
	e := newEnv(t)
	e.Runner.OnRun = func(string, []string) ([]byte, error) {
		return nil, errors.New("openscad failed: exit status 1\nERROR: Parser error in file \"model.scad\", line 1: syntax error")
	}
	if _, err := e.Svc.Compile(ctx, "cube", "cube(;", nil); err == nil || !strings.Contains(err.Error(), "syntax error") {
		t.Fatalf("got %v", err)
	}
	if _, err := e.Svc.Version("cube", 0); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("a failed compile left a version: %v", err)
	}
	runs, _ := e.Svc.Runs("cube", 5)
	if len(runs) != 1 || runs[0].ExitCode == nil || *runs[0].ExitCode != 1 {
		t.Fatalf("run log: %+v", runs)
	}
}

func TestPartThatDoesNotFitIsRefused(t *testing.T) {
	e := newEnv(t)
	r, err := e.Svc.Compile(ctx, "big", cube, map[string]any{"size": 300.0})
	if err != nil {
		t.Fatal(err)
	}
	if r.Fits || !strings.Contains(r.Summary(), "DOES NOT FIT") {
		t.Fatalf("%s", r.Summary())
	}
	if _, err := e.Svc.RequestApproval(ctx, "big", 0, "", slicer.Overrides{}); !errors.Is(err, pipeline.ErrTooBig) {
		t.Fatalf("want ErrTooBig, got %v", err)
	}
}

func TestEstimatesAreCachedAndReusedAtApproval(t *testing.T) {
	e := newEnv(t)
	e.Svc.Compile(ctx, "cube", cube, nil)
	e.Svc.Compile(ctx, "cube", "", map[string]any{"size": 30.0})
	ests, err := e.Svc.Estimate(ctx, "cube", []int{1, 2}, "", slicer.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ests) != 2 || ests[0].Grams >= ests[1].Grams {
		t.Fatalf("%+v", ests)
	}
	e.Svc.Estimate(ctx, "cube", []int{1, 2}, "", slicer.Overrides{})
	if e.Slicer.Calls() != 2 {
		t.Fatalf("sliced %d times, want 2 (cache)", e.Slicer.Calls())
	}
	if _, err := e.Svc.RequestApproval(ctx, "cube", 2, "", slicer.Overrides{}); err != nil {
		t.Fatal(err)
	}
	if e.Slicer.Calls() != 2 {
		t.Fatalf("approval sliced again (%d)", e.Slicer.Calls())
	}
	// A per-job override is a different slice.
	off := false
	e.Svc.Estimate(ctx, "cube", []int{2}, "", slicer.Overrides{Supports: &off})
	if e.Slicer.Calls() != 3 {
		t.Fatalf("override did not re-slice (%d)", e.Slicer.Calls())
	}
	vs, _ := e.Svc.Versions("cube")
	if len(vs) != 2 || len(vs[1].Estimates) != 2 {
		t.Fatalf("%+v", vs)
	}
}

func TestEstimateNeverCreatesAJob(t *testing.T) {
	e := newEnv(t)
	e.Svc.Compile(ctx, "cube", cube, nil)
	e.Svc.Estimate(ctx, "cube", nil, "", slicer.Overrides{})
	if p, _ := e.Svc.PendingJobs("cube"); len(p) != 0 {
		t.Fatalf("estimate created jobs: %+v", p)
	}
}

func TestStartEstimateRunsInBackground(t *testing.T) {
	e := newEnv(t)
	e.Svc.Compile(ctx, "cube", cube, nil)
	est, err := e.Svc.StartEstimate("cube", 1, "")
	if err != nil || est.Status != "running" {
		t.Fatalf("%+v %v", est, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		got, ok, _ := e.Svc.CachedEstimate("cube", 1, "")
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
	e.Config.Save(config.Defaults())
	e.Svc.Compile(ctx, "cube", cube, nil)
	if _, err := e.Svc.RequestApproval(ctx, "cube", 0, "", slicer.Overrides{}); !errors.Is(err, config.ErrNoPrinter) {
		t.Fatalf("got %v", err)
	}
}

func TestPrinterStatusIsWatchedDuringAPrint(t *testing.T) {
	e := newEnv(t)
	a := mustRequestApproval(t, e, "cube")
	e.Svc.Approve(a.Job.ID)
	if _, err := e.Svc.Print(ctx, a.Job.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if !e.Events.Has("printer") {
		t.Fatal("no printer status events")
	}
	if err := e.Svc.Cancel(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.Svc.Status(ctx, ""); st.State != printer.Idle {
		t.Fatalf("after cancel: %+v", st)
	}
}

func TestBadProjectName(t *testing.T) {
	e := newEnv(t)
	if _, err := e.Svc.Compile(ctx, "../data", cube, nil); !errors.Is(err, project.ErrBadName) {
		t.Fatalf("got %v", err)
	}
}

func TestRestoreMakesAnOldVersionCurrent(t *testing.T) {
	e := newEnv(t)
	e.Svc.Compile(ctx, "cube", cube, nil)
	e.Svc.Compile(ctx, "cube", "", map[string]any{"size": 40.0})
	r, err := e.Svc.Restore(ctx, "cube", 1)
	if err != nil {
		t.Fatal(err)
	}
	if r.Version != 3 || r.Size.X != 20 {
		t.Fatalf("%+v", r)
	}
}
