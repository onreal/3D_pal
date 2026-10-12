package bambustudio_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/margarit/printpipe/internal/slicers/bambustudio"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func library(t *testing.T) string {
	root := t.TempDir()
	write(t, filepath.Join(root, "filament", "fdm_filament_common.json"), `{"name":"fdm_filament_common","filament_density":["1"],"nozzle_temperature":["200"],"filament_diameter":["1.75"]}`)
	write(t, filepath.Join(root, "filament", "base", "pla.json"), `{"name":"fdm_filament_pla","inherits":"fdm_filament_common","filament_density":["1.26"],"filament_type":["PLA"]}`)
	write(t, filepath.Join(root, "filament", "Bambu PLA Basic @BBL A1.json"), `{"name":"Bambu PLA Basic @BBL A1","inherits":"fdm_filament_pla","from":"system","instantiation":"true","nozzle_temperature":["220"]}`)
	write(t, filepath.Join(root, "filament", "loop.json"), `{"name":"loop","inherits":"loop"}`)
	return root
}

func TestFlattenMergesTheChain(t *testing.T) {
	lib := bambustudio.NewLibrary(library(t))
	user := filepath.Join(t.TempDir(), "My PLA.json")
	write(t, user, `{"name":"My PLA","inherits":"Bambu PLA Basic @BBL A1","from":"User","nozzle_temperature":["215"]}`)
	got, err := lib.Flatten("filament", user)
	if err != nil {
		t.Fatal(err)
	}
	// Named after the system preset it derives from, so Bambu Studio's compatibility check passes.
	want := map[string]string{"name": "Bambu PLA Basic @BBL A1", "from": "system"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	if first(got["nozzle_temperature"]) != "215" || first(got["filament_density"]) != "1.26" || first(got["filament_diameter"]) != "1.75" || first(got["filament_type"]) != "PLA" {
		t.Fatalf("merged wrongly: %v", got)
	}
	if _, ok := got["inherits"]; ok {
		t.Fatal("inherits left in the flattened preset")
	}
}

func first(v any) any {
	if l, ok := v.([]any); ok && len(l) > 0 {
		return l[0]
	}
	return v
}

func TestFlattenUnknownParent(t *testing.T) {
	lib := bambustudio.NewLibrary(library(t))
	p := filepath.Join(t.TempDir(), "x.json")
	write(t, p, `{"name":"x","inherits":"Some Other Brand PLA"}`)
	if _, err := lib.Flatten("filament", p); err == nil || !strings.Contains(err.Error(), "Some Other Brand PLA") {
		t.Fatalf("got %v", err)
	}
	if _, err := lib.Flatten("filament", filepath.Join(library(t), "filament", "loop.json")); err == nil || !strings.Contains(err.Error(), "loop") {
		t.Fatalf("got %v", err)
	}
}

func TestFlattenWithoutLibraryKeepsSelfContainedPresets(t *testing.T) {
	lib := bambustudio.NewLibrary("")
	p := filepath.Join(t.TempDir(), "full.json")
	write(t, p, `{"name":"full","filament_density":["1.24"]}`)
	if got, err := lib.Flatten("filament", p); err != nil || got["name"] != "full" {
		t.Fatalf("%v %v", got, err)
	}
}
