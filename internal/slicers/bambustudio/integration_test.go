//go:build integration

package bambustudio_test

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/margarit/printpipe/internal/mesh"
	"github.com/margarit/printpipe/internal/runner"
	"github.com/margarit/printpipe/internal/slicer"
	"github.com/margarit/printpipe/internal/slicers/bambustudio"
)

// Slices a 20 mm cube with the real Bambu Studio and user presets that inherit A1 system
// presets, then checks the estimates and that the presets were really applied.
func TestRealBambuStudioSlice(t *testing.T) {
	dir := t.TempDir()
	stl := filepath.Join(dir, "model.stl")
	os.WriteFile(stl, mesh.Cube(20, 20, 20), 0o644)
	out := filepath.Join(dir, "slice", "job.gcode.3mf")
	profiles := map[string]string{
		slicer.ProfileMachine:  "../../../testdata/profiles/machine.json",
		slicer.ProfileProcess:  "../../../testdata/profiles/process.json",
		slicer.ProfileFilament: "../../../testdata/profiles/filament.json",
	}
	s := bambustudio.New(runner.Exec{}, os.Getenv("BAMBU_STUDIO"))
	res, err := s.Slice(context.Background(), slicer.Input{Model: stl, OutPath: out, Profiles: profiles})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%+v", res)
	if res.Seconds < 5*60 || res.Seconds > 60*60 {
		t.Errorf("a 20 mm cube should take 5 to 60 minutes, got %d s", res.Seconds)
	}
	if res.Layers != 100 || res.LayerHeight != 0.2 {
		t.Errorf("layers %d at %g mm, want 100 at 0.2", res.Layers, res.LayerHeight)
	}
	if len(res.Filaments) != 1 || res.Filaments[0].Type != "PLA" || res.Filaments[0].Grams < 2 || res.Filaments[0].Grams > 15 || res.Filaments[0].Metres <= 0 {
		t.Errorf("filament %+v", res.Filaments)
	}

	// The presets were applied: A1 printer, PLA temperature, supports on.
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	f, err := zr.Open("Metadata/project_settings.config")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(f)
	var ps map[string]any
	json.Unmarshal(raw, &ps)
	if ps["printer_model"] != "Bambu Lab A1" {
		t.Errorf("printer_model = %v", ps["printer_model"])
	}
	if ps["enable_support"] != "1" {
		t.Errorf("enable_support = %v, want the profile's 1", ps["enable_support"])
	}
	temp, _ := ps["nozzle_temperature"].([]any)
	if len(temp) == 0 || temp[0] != "220" {
		t.Errorf("nozzle_temperature = %v, want PLA Basic's 220", ps["nozzle_temperature"])
	}
}

func TestRealBambuStudioOverride(t *testing.T) {
	dir := t.TempDir()
	stl := filepath.Join(dir, "model.stl")
	os.WriteFile(stl, mesh.Cube(20, 20, 10), 0o644)
	profiles := map[string]string{
		slicer.ProfileMachine:  "../../../testdata/profiles/machine.json",
		slicer.ProfileProcess:  "../../../testdata/profiles/process.json",
		slicer.ProfileFilament: "../../../testdata/profiles/filament.json",
	}
	res, err := bambustudio.New(runner.Exec{}, os.Getenv("BAMBU_STUDIO")).Slice(context.Background(),
		slicer.Input{Model: stl, OutPath: filepath.Join(dir, "s", "job.gcode.3mf"), Profiles: profiles, Overrides: slicer.Overrides{LayerHeight: 0.1}})
	if err != nil {
		t.Fatal(err)
	}
	// The first layer stays at the profile's 0.2 mm: 1 + (10 - 0.2) / 0.1 = 99 layers.
	if res.Layers != 99 || res.LayerHeight != 0.1 {
		t.Fatalf("layers %d at %g mm, want 99 at 0.1", res.Layers, res.LayerHeight)
	}
}

// The real BBL folder offers the A1's own presets, and slicing with only the defaults (what a
// printer with no uploaded profiles does) works.
func TestRealSystemPresetsForTheA1(t *testing.T) {
	s := bambustudio.New(runner.Exec{}, os.Getenv("BAMBU_STUDIO"))
	ps := s.Presets("Bambu Lab A1")
	count := map[string]int{}
	for _, p := range ps {
		count[p.Kind]++
	}
	t.Logf("%d presets: %v", len(ps), count)
	if count[slicer.ProfileMachine] < 4 || count[slicer.ProfileProcess] < 5 || count[slicer.ProfileFilament] < 20 {
		t.Fatalf("too few A1 presets: %v", count)
	}
	d := slicer.Defaults(ps)
	want := map[string]string{slicer.ProfileMachine: "Bambu Lab A1 0.4 nozzle", slicer.ProfileProcess: "0.20mm Standard @BBL A1", slicer.ProfileFilament: "Bambu PLA Basic @BBL A1"}
	for kind, name := range want {
		if d[kind].Name != name {
			t.Errorf("default %s = %q, want %q", kind, d[kind].Name, name)
		}
	}
	if d[slicer.ProfileFilament].PricePerKg <= 0 {
		t.Errorf("no list price for %s", d[slicer.ProfileFilament].Name)
	}
	for _, p := range ps {
		if p.Name == "Bambu Lab A1 mini 0.4 nozzle" {
			t.Error("the A1 mini is another model")
		}
	}

	dir := t.TempDir()
	stl := filepath.Join(dir, "model.stl")
	os.WriteFile(stl, mesh.Cube(20, 20, 20), 0o644)
	profiles := map[string]string{}
	for kind, p := range d {
		profiles[kind] = p.Path
	}
	res, err := s.Slice(context.Background(), slicer.Input{Model: stl, OutPath: filepath.Join(dir, "slice", "job.gcode.3mf"), Profiles: profiles})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%+v", res)
	if res.Layers != 100 || len(res.Filaments) != 1 || res.Filaments[0].Type != "PLA" || res.Filaments[0].Grams < 2 {
		t.Errorf("%+v", res)
	}
}
