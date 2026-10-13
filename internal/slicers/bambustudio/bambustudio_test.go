package bambustudio_test

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/margarit/printpipe/internal/runner"
	"github.com/margarit/printpipe/internal/slicer"
	"github.com/margarit/printpipe/internal/slicers/bambustudio"
)

func TestArgsUseConfiguredProfiles(t *testing.T) {
	got := bambustudio.Args("/data/profiles/machine.json", "/data/profiles/process.json", "/data/profiles/filament.json",
		"/work/cube/v1/model.stl", "/work/cube/v1/job.gcode.3mf")
	want := []string{
		"--orient", "1", "--arrange", "1",
		"--load-settings", "/data/profiles/machine.json;/data/profiles/process.json",
		"--load-filaments", "/data/profiles/filament.json",
		"--slice", "0", "--outputdir", "/work/cube/v1", "--export-3mf", "job.gcode.3mf", "/work/cube/v1/model.stl",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("args mismatch\n got %q\nwant %q", got, want)
	}
}

func zipWith(t *testing.T, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "job.gcode.3mf")
	f, _ := os.Create(path)
	zw := zip.NewWriter(f)
	for name, body := range files {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	f.Close()
	return path
}

func TestEstimatesFromSlicedFile(t *testing.T) {
	path := zipWith(t, map[string]string{
		"Metadata/slice_info.config": `<?xml version="1.0" encoding="UTF-8"?><config><plate>` +
			`<metadata key="index" value="1"/><metadata key="prediction" value="1260"/>` +
			`<metadata key="weight" value="7.42"/><metadata key="support_used" value="true"/>` +
			`<metadata key="outside" value="false"/>` +
			`<filament id="1" type="PLA" color="#FFFFFF" used_m="2.48" used_g="7.42" used_for_support="true"/></plate></config>`,
		"Metadata/plate_1.gcode":           "; HEADER_BLOCK_START\n; BambuStudio 02.00\n; total layer number: 100\n; HEADER_BLOCK_END\nG28\n",
		"Metadata/project_settings.config": `{"layer_height": "0.2"}`,
	})
	res, err := bambustudio.ReadResult(path)
	if err != nil {
		t.Fatal(err)
	}
	want := slicer.Result{Path: path, Seconds: 1260, Layers: 100, LayerHeight: 0.2, Supports: true,
		Filaments: []slicer.Filament{{Type: "PLA", Grams: 7.42, Metres: 2.48}}}
	if res.Seconds != want.Seconds || res.Layers != want.Layers || res.LayerHeight != want.LayerHeight ||
		!res.Supports || !slices.Equal(res.Filaments, want.Filaments) {
		t.Fatalf("got %+v\nwant %+v", res, want)
	}
}

func TestReadResultNeedsSliceInfo(t *testing.T) {
	if _, err := bambustudio.ReadResult(zipWith(t, map[string]string{"x": "y"})); err == nil {
		t.Fatal("want error")
	}
}

func profiles(t *testing.T) map[string]string {
	dir := t.TempDir()
	m := map[string]string{}
	for _, k := range []string{slicer.ProfileMachine, slicer.ProfileProcess, slicer.ProfileFilament} {
		p := filepath.Join(dir, k+".json")
		os.WriteFile(p, []byte(`{"name":"`+k+`","enable_support":"0","layer_height":"0.2"}`), 0o644)
		m[k] = p
	}
	return m
}

func TestOverridesChangeTheProcessPreset(t *testing.T) {
	on, infill := true, 25
	p := map[string]any{"enable_support": "0", "layer_height": "0.2"}
	bambustudio.ApplyOverrides(p, slicer.Overrides{Supports: &on, SupportType: "tree(auto)", LayerHeight: 0.12, Infill: &infill, BedType: "Cool Plate"})
	if p["enable_support"] != "1" || p["support_type"] != "tree(auto)" || p["layer_height"] != "0.12" || p["sparse_infill_density"] != "25%" || p["curr_bed_type"] != "Cool Plate" {
		t.Fatalf("%v", p)
	}
	q := map[string]any{"layer_height": "0.2"}
	bambustudio.ApplyOverrides(q, slicer.Overrides{})
	if len(q) != 1 || q["layer_height"] != "0.2" {
		t.Fatalf("no overrides changed the preset: %v", q)
	}
}

func TestSliceGetsFlattenedPresetsWithOverrides(t *testing.T) {
	out := filepath.Join(t.TempDir(), "job.gcode.3mf")
	var args []string
	f := &runner.Fake{OnRun: func(_ string, a []string) ([]byte, error) {
		args = a
		return nil, errors.New("stop here")
	}}
	off := false
	bambustudio.New(f, "bs").WithLibrary(bambustudio.NewLibrary("")).
		Slice(context.Background(), slicer.Input{Model: "m.stl", OutPath: out, Profiles: profiles(t), Overrides: slicer.Overrides{Supports: &off}})
	dir := filepath.Dir(out)
	if len(args) < 6 || args[5] != filepath.Join(dir, "machine.json")+";"+filepath.Join(dir, "process.json") || args[7] != filepath.Join(dir, "filament.json") {
		t.Fatalf("args %q", args)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "process.json"))
	var p map[string]any
	json.Unmarshal(b, &p)
	if p["enable_support"] != "0" || p["name"] != "process" {
		t.Fatalf("job process preset: %v", p)
	}
}

func TestMissingProfilesFailClearly(t *testing.T) {
	s := bambustudio.New(&runner.Fake{}, "bs")
	_, err := s.Slice(context.Background(), slicer.Input{Model: "m.stl", OutPath: filepath.Join(t.TempDir(), "j.gcode.3mf")})
	if !errors.Is(err, bambustudio.ErrNoProfiles) {
		t.Fatalf("got %v", err)
	}
}

func TestSliceReportsCLIError(t *testing.T) {
	out := filepath.Join(t.TempDir(), "job.gcode.3mf")
	f := &runner.Fake{OnRun: func(string, []string) ([]byte, error) {
		os.WriteFile(filepath.Join(filepath.Dir(out), "result.json"), []byte(`{"return_code":-50,"error_string":"Some objects are too close"}`), 0o644)
		return nil, errors.New("bambu-studio failed: exit status 1")
	}}
	_, err := bambustudio.New(f, "bs").Slice(context.Background(), slicer.Input{Model: "m.stl", OutPath: out, Profiles: profiles(t)})
	if err == nil || !strings.Contains(err.Error(), "too close") {
		t.Fatalf("got %v", err)
	}
}

func TestSupportsMeansSupportWasPrinted(t *testing.T) {
	path := zipWith(t, map[string]string{
		"Metadata/slice_info.config": `<config><plate><metadata key="prediction" value="747"/><metadata key="support_used" value="true"/>` +
			`<filament id="1" type="PLA" used_m="1.23" used_g="3.72" used_for_support="false"/></plate></config>`,
	})
	res, err := bambustudio.ReadResult(path)
	if err != nil || res.Supports {
		t.Fatalf("a cube with supports enabled printed none: %+v %v", res, err)
	}
}
