package bambustudio_test

import (
	"path/filepath"
	"testing"

	"github.com/margarit/printpipe/internal/slicer"
	"github.com/margarit/printpipe/internal/slicers/bambustudio"
)

// presetLibrary is a cut-down BBL folder: two A1 nozzles, a P1S, and processes and filaments for each.
func presetLibrary(t *testing.T) string {
	root := library(t)
	m := filepath.Join(root, "machine")
	write(t, filepath.Join(m, "fdm_bbl_3dp_001_common.json"), `{"name":"fdm_bbl_3dp_001_common","from":"system","instantiation":"false"}`)
	write(t, filepath.Join(m, "Bambu Lab A1 0.4 nozzle.json"), `{"type":"machine","name":"Bambu Lab A1 0.4 nozzle","inherits":"fdm_bbl_3dp_001_common","from":"system","instantiation":"true","printer_model":"Bambu Lab A1","printer_variant":"0.4","default_print_profile":"0.20mm Standard @BBL A1","default_filament_profile":["Bambu PLA Basic @BBL A1"]}`)
	write(t, filepath.Join(m, "Bambu Lab A1 0.2 nozzle.json"), `{"type":"machine","name":"Bambu Lab A1 0.2 nozzle","from":"system","instantiation":"true","printer_model":"Bambu Lab A1","printer_variant":"0.2","default_print_profile":"0.10mm Standard @BBL A1 0.2 nozzle"}`)
	write(t, filepath.Join(m, "Bambu Lab P1S 0.4 nozzle.json"), `{"type":"machine","name":"Bambu Lab P1S 0.4 nozzle","from":"system","instantiation":"true","printer_model":"Bambu Lab P1S","printer_variant":"0.4"}`)
	p := filepath.Join(root, "process")
	write(t, filepath.Join(p, "fdm_process_common.json"), `{"name":"fdm_process_common","instantiation":"false"}`)
	write(t, filepath.Join(p, "0.20mm Standard @BBL A1.json"), `{"type":"process","name":"0.20mm Standard @BBL A1","inherits":"fdm_process_common","from":"system","instantiation":"true","compatible_printers":["Bambu Lab A1 0.4 nozzle"]}`)
	write(t, filepath.Join(p, "0.12mm Fine @BBL A1.json"), `{"type":"process","name":"0.12mm Fine @BBL A1","from":"system","instantiation":"true","compatible_printers":["Bambu Lab A1 0.4 nozzle"]}`)
	write(t, filepath.Join(p, "0.20mm Standard @BBL P1P.json"), `{"type":"process","name":"0.20mm Standard @BBL P1P","from":"system","instantiation":"true","compatible_printers":["Bambu Lab P1S 0.4 nozzle"]}`)
	f := filepath.Join(root, "filament")
	// The price and the compatible printers come from the parent: the catalogue flattens to find them.
	write(t, filepath.Join(f, "Bambu PLA Basic @base.json"), `{"name":"Bambu PLA Basic @base","inherits":"fdm_filament_pla","instantiation":"false","filament_cost":["19.99"]}`)
	write(t, filepath.Join(f, "Bambu PLA Basic @BBL A1.json"), `{"type":"filament","name":"Bambu PLA Basic @BBL A1","inherits":"Bambu PLA Basic @base","from":"system","instantiation":"true","compatible_printers":["Bambu Lab A1 0.4 nozzle"]}`)
	write(t, filepath.Join(f, "Bambu PETG HF @base.json"), `{"name":"Bambu PETG HF @base","instantiation":"false","compatible_printers":["Bambu Lab A1 0.4 nozzle","Bambu Lab A1 0.2 nozzle"],"filament_cost":["21.5"]}`)
	write(t, filepath.Join(f, "Bambu PETG HF @BBL A1.json"), `{"type":"filament","name":"Bambu PETG HF @BBL A1","inherits":"Bambu PETG HF @base","from":"system","instantiation":"true"}`)
	write(t, filepath.Join(f, "Bambu PLA Basic @BBL P1S.json"), `{"type":"filament","name":"Bambu PLA Basic @BBL P1S","inherits":"Bambu PLA Basic @base","from":"system","instantiation":"true","compatible_printers":["Bambu Lab P1S 0.4 nozzle"]}`)
	return root
}

func TestPresetsForTheA1(t *testing.T) {
	lib := bambustudio.NewLibrary(presetLibrary(t))
	ps := lib.Presets("Bambu Lab A1")
	got := map[string]slicer.Preset{}
	for _, p := range ps {
		got[p.Name] = p
		if p.Path == "" {
			t.Errorf("%s has no file", p.Name)
		}
	}
	for _, name := range []string{"Bambu Lab A1 0.4 nozzle", "Bambu Lab A1 0.2 nozzle", "0.20mm Standard @BBL A1", "0.12mm Fine @BBL A1", "Bambu PLA Basic @BBL A1", "Bambu PETG HF @BBL A1"} {
		if _, ok := got[name]; !ok {
			t.Errorf("missing %s", name)
		}
	}
	for _, name := range []string{"Bambu Lab P1S 0.4 nozzle", "0.20mm Standard @BBL P1P", "Bambu PLA Basic @BBL P1S", "fdm_process_common", "Bambu PLA Basic @base"} {
		if _, ok := got[name]; ok {
			t.Errorf("%s is not an A1 preset", name)
		}
	}
	if len(ps) != 6 {
		t.Errorf("got %d presets", len(ps))
	}
	d := slicer.Defaults(ps)
	want := map[string]string{"machine": "Bambu Lab A1 0.4 nozzle", "process": "0.20mm Standard @BBL A1", "filament": "Bambu PLA Basic @BBL A1"}
	for kind, name := range want {
		if d[kind].Name != name {
			t.Errorf("default %s = %q, want %q", kind, d[kind].Name, name)
		}
	}
	if d["filament"].PricePerKg != 19.99 || got["Bambu PETG HF @BBL A1"].PricePerKg != 21.5 {
		t.Errorf("prices: %v %v", d["filament"].PricePerKg, got["Bambu PETG HF @BBL A1"].PricePerKg)
	}
	// Machines first, then processes, then filaments; the default first in each.
	if ps[0].Name != "Bambu Lab A1 0.4 nozzle" || ps[2].Name != "0.20mm Standard @BBL A1" || ps[4].Name != "Bambu PLA Basic @BBL A1" {
		t.Errorf("order: %v", ps)
	}
}

func TestPresetsUnknownModelOrNoLibrary(t *testing.T) {
	if ps := bambustudio.NewLibrary(presetLibrary(t)).Presets("Prusa MK4"); len(ps) != 0 {
		t.Fatalf("got %v", ps)
	}
	if ps := bambustudio.NewLibrary("").Presets("Bambu Lab A1"); len(ps) != 0 {
		t.Fatalf("got %v", ps)
	}
}
