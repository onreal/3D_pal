package pipeline_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"slices"
	"sync"
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
	runs, _ := e.Svc.Runs(pipeline.RunFilter{Project: "cube"}, 5)
	if len(runs) != 1 || runs[0].ExitCode == nil || *runs[0].ExitCode != 1 {
		t.Fatalf("run log: %+v", runs)
	}
}

// Every compile is bracketed by render events, so the viewer can show a render in progress
// whoever started it, and clear it when it ends, failed or not.
func TestCompileIsBracketedByRenderEvents(t *testing.T) {
	e := newEnv(t)
	var mu sync.Mutex
	var seen []map[string]any
	e.Events.Next = func(typ string, v any) {
		if m, ok := v.(map[string]any); ok && typ == "render" {
			mu.Lock()
			seen = append(seen, m)
			mu.Unlock()
		}
	}
	if _, err := e.Svc.Compile(ctx, "cube", cube, nil); err != nil {
		t.Fatal(err)
	}
	e.Runner.OnRun = func(string, []string) ([]byte, error) {
		return nil, errors.New("openscad failed: exit status 1\nERROR: syntax error")
	}
	if _, err := e.Svc.Compile(ctx, "cube", "cube(;", nil); err == nil {
		t.Fatal("expected the broken model to fail")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 4 {
		t.Fatalf("render events: %+v", seen)
	}
	if seen[0]["state"] != "started" || seen[1]["state"] != "done" || seen[1]["version"] != 1 || seen[0]["id"] != seen[1]["id"] {
		t.Fatalf("successful render: %+v %+v", seen[0], seen[1])
	}
	if seen[2]["state"] != "started" || seen[3]["state"] != "failed" || !strings.Contains(seen[3]["error"].(string), "syntax error") || seen[2]["id"] != seen[3]["id"] {
		t.Fatalf("failed render: %+v %+v", seen[2], seen[3])
	}
	if seen[0]["id"] == seen[2]["id"] || seen[0]["project"] != "cube" {
		t.Fatalf("renders must be told apart: %+v", seen)
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

func TestPrintersTestAndListings(t *testing.T) {
	e := newEnv(t)
	ps, err := e.Svc.Printers(ctx, true)
	if err != nil || len(ps) != 1 || !ps[0].Default || ps[0].Status == nil || ps[0].Status.State != printer.Idle {
		t.Fatalf("%+v %v", ps, err)
	}
	checks, err := e.Svc.TestPrinter(ctx, "sim")
	if err != nil || len(checks) != 1 || !checks[0].OK {
		t.Fatalf("%+v %v", checks, err)
	}
	if _, err := e.Svc.TestPrinter(ctx, "nope"); !errors.Is(err, config.ErrUnknownPrinter) {
		t.Fatalf("got %v", err)
	}
	e.Svc.Compile(ctx, "cube", cube, map[string]any{"size": 20.0})
	projects, _ := e.Svc.Projects()
	if len(projects) != 1 || projects[0].Name != "cube" {
		t.Fatalf("%+v", projects)
	}
	a, _ := e.Svc.RequestApproval(ctx, "cube", 0, "sim", slicer.Overrides{})
	if j, err := e.Svc.Job(a.Job.ID); err != nil || j.Version != 1 || len(j.Estimate) == 0 {
		t.Fatalf("%+v %v", j, err)
	}
	if _, err := e.Svc.Job("deadbeef"); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestSummaryMentionsEstimateAndWarnings(t *testing.T) {
	r := pipeline.CompileResult{Project: "box", Version: 2, Params: map[string]any{"label": "A1", "wall": 2.0},
		Warnings: []string{"the top preview was not written"}, Messages: []string{"wall check ok"}, ViewerURL: "http://localhost:8080"}
	s := r.Summary()
	for _, want := range []string{`label="A1"`, "wall=2", "Warning: the top preview", "Model output (echo and OpenSCAD warnings):\n  wall check ok", "localhost:8080"} {
		if !strings.Contains(s, want) {
			t.Errorf("summary missing %q: %s", want, s)
		}
	}
}

func TestEstimateErrorsAreReported(t *testing.T) {
	e := newEnv(t)
	if _, err := e.Svc.Estimate(ctx, "cube", nil, "", slicer.Overrides{}); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	e.Svc.Compile(ctx, "cube", cube, nil)
	if _, err := e.Svc.Estimate(ctx, "cube", nil, "nope", slicer.Overrides{}); !errors.Is(err, config.ErrUnknownPrinter) {
		t.Fatalf("got %v", err)
	}
	if _, err := e.Svc.StartEstimate("cube", 9, ""); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if _, _, err := e.Svc.CachedEstimate("cube", 1, "nope"); !errors.Is(err, config.ErrUnknownPrinter) {
		t.Fatalf("got %v", err)
	}
	if _, err := e.Svc.Restore(ctx, "cube", 7); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestBusyPrinterIsNotUploadedTo(t *testing.T) {
	e := newEnv(t)
	a := mustRequestApproval(t, e, "cube")
	e.Svc.Approve(a.Job.ID)
	e.Svc.Print(ctx, a.Job.ID)
	b, _ := e.Svc.RequestApproval(ctx, "cube", 0, "", slicer.Overrides{})
	e.Svc.Approve(b.Job.ID)
	if _, err := e.Svc.Print(ctx, b.Job.ID); !errors.Is(err, printer.ErrBusy) {
		t.Fatalf("want ErrBusy, got %v", err)
	}
	if e.Printer.Uploads() != 1 {
		t.Fatal("uploaded to a busy printer")
	}
}

func TestDefaultPrinterBuiltFromRegistry(t *testing.T) {
	e := newEnv(t)
	svc := pipeline.New(pipeline.Deps{DB: storeOf(t), Config: e.Config, Events: e.Events, Projects: project.NewStore(storeOf(t), t.TempDir())})
	ps, err := svc.Printers(ctx, false)
	if err != nil || len(ps) != 1 || ps[0].Capabilities.Model != "Fake printer" {
		t.Fatalf("%+v %v", ps, err)
	}
}

func TestRunsCutOffByARestartAreMarked(t *testing.T) {
	db := storeOf(t)
	db.Exec(`INSERT INTO runs (project, tool, started_at) VALUES ('cube', 'compile_model', '2026-10-12T10:00:00Z')`)
	e := newEnv(t)
	svc := pipeline.New(pipeline.Deps{DB: db, Config: e.Config, Events: e.Events, Projects: project.NewStore(db, t.TempDir())})
	runs, _ := svc.Runs(pipeline.RunFilter{Project: "cube"}, 5)
	if len(runs) != 1 || runs[0].ExitCode == nil || *runs[0].ExitCode != 1 || !strings.Contains(runs[0].Message, "interrupted") {
		t.Fatalf("%+v", runs)
	}
}

// shipPresets gives the test slicer three default presets and one more filament, as files.
func shipPresets(t *testing.T, e *env) {
	dir := t.TempDir()
	preset := func(kind, name string, def bool, price float64) slicer.Preset {
		path := filepath.Join(dir, name+".json")
		os.WriteFile(path, []byte(`{"name":"`+name+`","from":"system","instantiation":"true"}`), 0o644)
		return slicer.Preset{Kind: kind, Name: name, Path: path, Default: def, PricePerKg: price}
	}
	e.Slicer.Shipped = []slicer.Preset{
		preset("machine", "Bambu Lab A1 0.4 nozzle", true, 0),
		preset("process", "0.20mm Standard @BBL A1", true, 0),
		preset("filament", "Bambu PLA Basic @BBL A1", true, 20),
		preset("filament", "Bambu PETG HF @BBL A1", false, 25),
	}
}

func TestPrinterWithoutProfilesSlicesWithTheShippedDefaults(t *testing.T) {
	e := newEnv(t)
	shipPresets(t, e)
	e.Svc.Compile(ctx, "cube", cube, nil)
	ests, err := e.Svc.Estimate(ctx, "cube", []int{1}, "", slicer.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	got := e.Slicer.Last().Profiles
	for kind, name := range map[string]string{"machine": "Bambu Lab A1 0.4 nozzle", "process": "0.20mm Standard @BBL A1", "filament": "Bambu PLA Basic @BBL A1"} {
		if filepath.Base(got[kind]) != name+".json" {
			t.Errorf("%s profile = %q", kind, got[kind])
		}
	}
	if ests[0].Cost == 0 {
		t.Error("no cost from the default filament's list price")
	}
	ps, _ := e.Svc.Printers(ctx, false)
	if ps[0].SystemProfiles["filament"] != "Bambu PLA Basic @BBL A1" {
		t.Errorf("printer info: %+v", ps[0].SystemProfiles)
	}

	// An uploaded profile wins over the default of its kind; the others stay.
	if _, err := e.Config.SaveProfile("filament", "Mine.json", []byte(`{"name":"Mine"}`)); err != nil {
		t.Fatal(err)
	}
	set, _ := e.Config.Load()
	set.Printers[0].Profiles = map[string]string{"filament": "Mine.json"}
	if err := e.Config.Save(set); err != nil {
		t.Fatal(err)
	}
	e.Svc.Estimate(ctx, "cube", []int{1}, "", slicer.Overrides{})
	got = e.Slicer.Last().Profiles
	if filepath.Base(got["filament"]) != "Mine.json" || filepath.Base(got["machine"]) != "Bambu Lab A1 0.4 nozzle.json" {
		t.Errorf("profiles = %v", got)
	}
	ps, _ = e.Svc.Printers(ctx, false)
	if _, ok := ps[0].SystemProfiles["filament"]; ok || ps[0].SystemProfiles["process"] == "" {
		t.Errorf("printer info: %+v", ps[0].SystemProfiles)
	}
}

func TestImportPreset(t *testing.T) {
	e := newEnv(t)
	shipPresets(t, e)
	ps, err := e.Svc.SystemPresets("")
	if err != nil || len(ps) != 4 {
		t.Fatalf("%v %v", ps, err)
	}
	prof, err := e.Svc.ImportPreset("sim", "filament", "Bambu PETG HF @BBL A1")
	if err != nil {
		t.Fatal(err)
	}
	if prof.Name != "Bambu PETG HF BBL A1.json" || prof.Kind != "filament" || prof.PricePerKg != 25 {
		t.Fatalf("%+v", prof)
	}
	set, _ := e.Config.Load()
	if p, ok := set.Profile(prof.Name); !ok || p.PricePerKg != 25 {
		t.Fatalf("%+v", set.Profiles)
	}
	// Importing again keeps a price the user changed.
	e.Config.SetProfilePrice(prof.Name, 30)
	if again, _ := e.Svc.ImportPreset("sim", "filament", "Bambu PETG HF @BBL A1"); again.PricePerKg != 30 {
		t.Fatalf("%+v", again)
	}
	for _, bad := range [][2]string{{"filament", "Nope"}, {"process", "Bambu PETG HF @BBL A1"}} {
		if _, err := e.Svc.ImportPreset("sim", bad[0], bad[1]); err == nil {
			t.Errorf("imported %v", bad)
		}
	}
}

func TestConcurrentSendsPrintOnce(t *testing.T) {
	e := newEnv(t)
	a := mustRequestApproval(t, e, "cube")
	e.Svc.Approve(a.Job.ID)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.Svc.Print(ctx, a.Job.ID)
			if err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			} else if !errors.Is(err, pipeline.ErrSending) && !errors.Is(err, jobs.ErrPrinted) {
				t.Errorf("unexpected %v", err)
			}
		}()
	}
	wg.Wait()
	if ok != 1 || e.Printer.Starts() != 1 {
		t.Fatalf("sent %d times, %d starts", ok, e.Printer.Starts())
	}
}

func TestRefusedStartIsNotPrinted(t *testing.T) {
	e := newEnv(t)
	a := mustRequestApproval(t, e, "cube")
	e.Svc.Approve(a.Job.ID)
	e.Printer.RefuseStart(printer.ErrRefused)
	if _, err := e.Svc.Print(ctx, a.Job.ID); !errors.Is(err, printer.ErrRefused) {
		t.Fatalf("got %v", err)
	}
	if j, _ := e.Svc.Job(a.Job.ID); j.Printed {
		t.Fatal("a refused job was marked printed")
	}
	// Once the printer accepts (Developer Mode on), the same approved job sends.
	e.Printer.RefuseStart(nil)
	if _, err := e.Svc.Print(ctx, a.Job.ID); err != nil {
		t.Fatal(err)
	}
}

func TestFollowingAPrint(t *testing.T) {
	e := newEnv(t)
	var mu sync.Mutex
	var seen []map[string]any
	e.Events.Next = func(typ string, v any) {
		if m, ok := v.(map[string]any); ok && typ == "printer" {
			mu.Lock()
			seen = append(seen, m)
			mu.Unlock()
		}
	}
	a := mustRequestApproval(t, e, "cube")
	e.Svc.Approve(a.Job.ID)
	if _, err := e.Svc.Print(ctx, a.Job.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	mu.Lock()
	var withJob int
	for _, m := range seen {
		if j, ok := m["print_job"].(jobs.Job); ok && j.ID == a.Job.ID {
			withJob++
		}
	}
	mu.Unlock()
	if withJob == 0 {
		t.Fatal("status events do not name the job being printed")
	}
	ps, _ := e.Svc.Printers(ctx, true)
	if ps[0].Job == nil || ps[0].Job.ID != a.Job.ID || ps[0].Status.JobName != "cube_v1" || ps[0].Status.Remaining <= 0 {
		t.Fatalf("%+v %+v", ps[0].Job, ps[0].Status)
	}

	// A restarted server finds the job from the name the printer reports.
	again := pipeline.New(e.Deps)
	defer again.Close()
	ps, _ = again.Printers(ctx, true)
	if ps[0].Job == nil || ps[0].Job.ID != a.Job.ID {
		t.Fatalf("after a restart: %+v", ps[0].Job)
	}
	// Idle printers follow nothing.
	e.Svc.Cancel(ctx, "")
	if ps, _ = again.Printers(ctx, true); ps[0].Job != nil {
		t.Fatalf("idle printer has job %+v", ps[0].Job)
	}
}

func TestWatchRidesOutAStatusOutage(t *testing.T) {
	e := newEnv(t)
	var mu sync.Mutex
	var states []printer.State
	e.Events.Next = func(typ string, v any) {
		if m, ok := v.(map[string]any); ok && typ == "printer" {
			mu.Lock()
			states = append(states, m["status"].(printer.Status).State)
			mu.Unlock()
		}
	}
	a := mustRequestApproval(t, e, "cube")
	e.Svc.Approve(a.Job.ID)
	if _, err := e.Svc.Print(ctx, a.Job.ID); err != nil {
		t.Fatal(err)
	}
	e.Printer.StatusDown(printer.ErrUnreachable)
	time.Sleep(60 * time.Millisecond) // many polls fail
	ps, _ := e.Svc.Printers(ctx, true)
	if ps[0].Status == nil || ps[0].Status.State != printer.Unknown || ps[0].Error == "" {
		t.Fatalf("printers during the outage: %+v %q", ps[0].Status, ps[0].Error)
	}
	e.Printer.StatusDown(nil)
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if !slices.Contains(states, printer.Unknown) || states[len(states)-1] != printer.Printing {
		t.Fatalf("states %v: want unknown during the outage, then printing again from the same watch", states)
	}
}

func TestAPrintStartedWithoutConfirmationIsAdopted(t *testing.T) {
	e := newEnv(t)
	a := mustRequestApproval(t, e, "cube")
	e.Svc.Approve(a.Job.ID)
	// The start command reached the printer but its confirmation was lost: the job was
	// never marked printed, yet the printer runs it.
	r, _ := e.Printer.Upload(ctx, printer.Job{Path: a.Job.FilePath, Name: "cube_v1.gcode.3mf"})
	if err := e.Printer.Start(ctx, r); err != nil {
		t.Fatal(err)
	}
	ps, _ := e.Svc.Printers(ctx, true)
	if ps[0].Job == nil || ps[0].Job.ID != a.Job.ID || !ps[0].Job.Printed {
		t.Fatalf("running job not adopted: %+v", ps[0].Job)
	}
	if j, _ := e.Svc.Job(a.Job.ID); !j.Printed {
		t.Fatal("adopted job not marked printed")
	}
	if !e.Events.Has("job") {
		t.Fatal("no job event for the viewer's panel")
	}
	if pending, _ := e.Svc.PendingJobs("cube"); len(pending) != 0 {
		t.Fatalf("adopted job still offered for sending: %+v", pending)
	}
}
