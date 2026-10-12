// Package printertest is the contract every printer driver must pass (PRD FR-27).
package printertest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/margarit/printpipe/internal/printer"
)

// Harness builds the driver under test, wired to fakes of its transport.
type Harness struct {
	// New returns a working printer.
	New func(t *testing.T) printer.Printer
	// Broken returns a printer whose transport fails the way want describes:
	// printer.ErrUnreachable, printer.ErrAuth or printer.ErrNoStorage.
	Broken func(t *testing.T, want error) printer.Printer
}

// Run checks behaviour every driver must have.
func Run(t *testing.T, h Harness) {
	ctx := context.Background()

	t.Run("capabilities are complete", func(t *testing.T) {
		c := h.New(t).Capabilities()
		if c.Model == "" || c.JobFormat == "" || c.Slicer == "" {
			t.Fatalf("model, job format and slicer are required: %+v", c)
		}
		if c.BuildVolume.X <= 0 || c.BuildVolume.Y <= 0 || c.BuildVolume.Z <= 0 {
			t.Fatalf("build volume must be positive: %+v", c.BuildVolume)
		}
		if c.BedShape != "rect" && c.BedShape != "round" {
			t.Fatalf("bed shape must be rect or round, got %q", c.BedShape)
		}
	})

	t.Run("connection test passes", func(t *testing.T) {
		if err := h.New(t).Test(ctx); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("start of an unknown remote fails", func(t *testing.T) {
		if err := h.New(t).Start(ctx, printer.Remote{ID: "never-uploaded.gcode.3mf"}); err == nil {
			t.Fatal("start of a job that was never uploaded succeeded")
		}
	})

	t.Run("upload then start succeeds", func(t *testing.T) {
		p := h.New(t)
		r, err := p.Upload(ctx, jobFile(t, p))
		if err != nil {
			t.Fatal(err)
		}
		if r.ID == "" {
			t.Fatal("upload returned an empty remote")
		}
		if err := p.Start(ctx, r); err != nil {
			t.Fatal(err)
		}
		if p.Capabilities().Has(printer.FeatureStatus) {
			st, err := p.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if st.State != printer.Printing {
				t.Fatalf("status after start: %+v", st)
			}
		}
	})

	t.Run("optional features return ErrUnsupported", func(t *testing.T) {
		p := h.New(t)
		c := p.Capabilities()
		if !c.Has(printer.FeatureStatus) {
			if _, err := p.Status(ctx); !errors.Is(err, printer.ErrUnsupported) {
				t.Fatalf("Status without the feature: want ErrUnsupported, got %v", err)
			}
		}
		if !c.Has(printer.FeatureCancel) {
			if err := p.Cancel(ctx); !errors.Is(err, printer.ErrUnsupported) {
				t.Fatalf("Cancel without the feature: want ErrUnsupported, got %v", err)
			}
		}
	})

	t.Run("cancel when idle is safe", func(t *testing.T) {
		err := h.New(t).Cancel(ctx)
		if err != nil && !errors.Is(err, printer.ErrUnsupported) {
			t.Fatalf("cancel when idle: %v", err)
		}
	})

	t.Run("transport errors map to shared errors", func(t *testing.T) {
		if h.Broken == nil {
			t.Fatal("Harness.Broken is required")
		}
		for _, want := range []error{printer.ErrUnreachable, printer.ErrAuth, printer.ErrNoStorage} {
			p := h.Broken(t, want)
			var err error
			if want == printer.ErrNoStorage {
				_, err = p.Upload(ctx, jobFile(t, p))
			} else {
				err = p.Test(ctx)
			}
			if !errors.Is(err, want) {
				t.Errorf("want %v, got %v", want, err)
			}
		}
	})
}

func jobFile(t *testing.T, p printer.Printer) printer.Job {
	t.Helper()
	name := "contract" + p.Capabilities().JobFormat
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("job bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	return printer.Job{Path: path, Name: name}
}
