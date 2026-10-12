package estimates_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/margarit/printpipe/internal/estimates"
	"github.com/margarit/printpipe/internal/slicer"
	"github.com/margarit/printpipe/internal/store/storetest"
)

var ctx = context.Background()

type fakeSlicer struct {
	calls atomic.Int32
	path  string
	delay time.Duration
	err   error
}

func (f *fakeSlicer) slice(context.Context) (slicer.Result, error) {
	f.calls.Add(1)
	time.Sleep(f.delay)
	if f.err != nil {
		return slicer.Result{}, f.err
	}
	return slicer.Result{Path: f.path, Seconds: 1260, Layers: 100, LayerHeight: 0.2,
		Filaments: []slicer.Filament{{Type: "PLA", Grams: 7.42, Metres: 2.48}}}, nil
}

func setup(t *testing.T) (*estimates.Service, *fakeSlicer) {
	db := storetest.Open(t)
	// estimates reference a version row
	db.Exec(`INSERT INTO projects (name) VALUES ('cube')`)
	db.Exec(`INSERT INTO versions (project, version, scad_path, stl_path, png_path, stl_sha256) VALUES ('cube', 1, '', '', '', 'abc')`)
	path := filepath.Join(t.TempDir(), "job.gcode.3mf")
	os.WriteFile(path, []byte("x"), 0o644)
	return estimates.New(db, nil), &fakeSlicer{path: path}
}

var in = estimates.Input{Project: "cube", Version: 1, STLHash: "abc", PrinterID: "a1", ProfileHashes: "p1"}

func TestEstimateIsCachedByInputs(t *testing.T) {
	svc, sl := setup(t)
	first, err := svc.Get(ctx, in, sl.slice)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := svc.Get(ctx, in, sl.slice)
	if sl.calls.Load() != 1 {
		t.Fatalf("sliced %d times, want 1", sl.calls.Load())
	}
	if first.Minutes != second.Minutes || first.Grams != second.Grams || first.Key != second.Key {
		t.Fatalf("cached estimate differs: %+v vs %+v", first, second)
	}
}

func TestChangedProfileRecomputes(t *testing.T) {
	svc, sl := setup(t)
	svc.Get(ctx, in, sl.slice)
	changed := in
	changed.ProfileHashes = "p2"
	svc.Get(ctx, changed, sl.slice)
	if sl.calls.Load() != 2 {
		t.Fatalf("sliced %d times, want 2", sl.calls.Load())
	}
}

func TestConcurrentRequestsShareOneRun(t *testing.T) {
	svc, sl := setup(t)
	sl.delay = 50 * time.Millisecond
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); svc.Get(ctx, in, sl.slice) }()
	}
	wg.Wait()
	if sl.calls.Load() != 1 {
		t.Fatalf("sliced %d times, want 1", sl.calls.Load())
	}
}

func TestCostOnlyWhenPriceIsSet(t *testing.T) {
	r := slicer.Result{Seconds: 1260, Filaments: []slicer.Filament{{Type: "PLA", Grams: 7.42, Metres: 2.48}}}
	est := estimates.Summarise(r, 25.0) // per kg
	if est.Minutes != 21 || est.Grams != 7.4 || est.Cost != 0.19 || est.Metres != 2.48 || est.Filament != "PLA" {
		t.Fatalf("got %+v", est)
	}
	if estimates.Summarise(r, 0).Cost != 0 {
		t.Fatal("cost without a price")
	}
}

func TestStartRunsInBackgroundAndNotifies(t *testing.T) {
	db := storetest.Open(t)
	db.Exec(`INSERT INTO projects (name) VALUES ('cube')`)
	db.Exec(`INSERT INTO versions (project, version, scad_path, stl_path, png_path, stl_sha256) VALUES ('cube', 1, '', '', '', 'abc')`)
	done := make(chan estimates.Estimate, 1)
	svc := estimates.New(db, func(e estimates.Estimate) { done <- e })
	path := filepath.Join(t.TempDir(), "job.gcode.3mf")
	os.WriteFile(path, []byte("x"), 0o644)
	sl := &fakeSlicer{path: path, delay: 20 * time.Millisecond}
	e, err := svc.Start(in, sl.slice)
	if err != nil || e.Status != "running" {
		t.Fatalf("%+v %v", e, err)
	}
	got := <-done
	if got.Status != "done" || got.Minutes != 21 {
		t.Fatalf("%+v", got)
	}
	again, _ := svc.Start(in, sl.slice)
	if again.Status != "done" || sl.calls.Load() != 1 {
		t.Fatalf("not served from cache: %+v", again)
	}
	list, _ := svc.ForVersion("cube", 1, func(string) (float64, string) { return 25, "EUR" })
	if len(list) != 1 || list[0].Cost != 0.19 || list[0].Currency != "EUR" {
		t.Fatalf("%+v", list)
	}
}

func TestFailedEstimateIsRetried(t *testing.T) {
	svc, sl := setup(t)
	sl.err = errors.New("slicer crashed")
	if _, err := svc.Get(ctx, in, sl.slice); err == nil {
		t.Fatal("want error")
	}
	sl.err = nil
	if e, err := svc.Get(ctx, in, sl.slice); err != nil || e.Status != "done" {
		t.Fatalf("%+v %v", e, err)
	}
}
