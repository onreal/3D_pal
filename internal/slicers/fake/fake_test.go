package fake_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/margarit/printpipe/internal/mesh"
	"github.com/margarit/printpipe/internal/slicer"
	"github.com/margarit/printpipe/internal/slicers/bambustudio"
	"github.com/margarit/printpipe/internal/slicers/fake"
)

func TestFakeWritesAReadableJob(t *testing.T) {
	dir := t.TempDir()
	stl := filepath.Join(dir, "model.stl")
	os.WriteFile(stl, mesh.Cube(20, 20, 20), 0o644)
	out := filepath.Join(dir, "job.gcode.3mf")
	res, err := fake.Slicer{}.Slice(context.Background(), slicer.Input{Model: stl, OutPath: out})
	if err != nil {
		t.Fatal(err)
	}
	read, err := bambustudio.ReadResult(out)
	if err != nil {
		t.Fatal(err)
	}
	if read.Seconds != res.Seconds || read.Layers != 100 || read.LayerHeight != 0.2 || read.Filaments[0].Grams != res.Filaments[0].Grams {
		t.Fatalf("written %+v, read back %+v", res, read)
	}
	if g := res.Filaments[0].Grams; g < 4 || g > 10 {
		t.Fatalf("a 20 mm cube should be a few grams, got %v", g)
	}
}
