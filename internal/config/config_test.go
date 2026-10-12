package config_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/margarit/printpipe/internal/config"
	_ "github.com/margarit/printpipe/internal/printers/bambu"
	_ "github.com/margarit/printpipe/internal/printers/fake"
	"github.com/margarit/printpipe/internal/store/storetest"
)

func newStore(t *testing.T) *config.Store {
	return config.NewStore(storetest.Open(t), filepath.Join(t.TempDir(), "profiles"))
}

func a1() config.Printer {
	return config.Printer{ID: "a1", Name: "Workshop A1", Kind: "bambu-lan", Default: true,
		Config:   map[string]string{"ip": "192.168.1.50", "serial": "03919C000000000", "access_code": "12345678"},
		Profiles: map[string]string{}}
}

func TestDefaultsAreValid(t *testing.T) {
	if err := config.Defaults().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestFreshStoreLoadsDefaults(t *testing.T) {
	s, err := newStore(t).Load()
	if err != nil {
		t.Fatal(err)
	}
	if !s.Pipeline.ApprovalRequired || s.Viewer.OverhangAngle != 45 || len(s.Printers) != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	st := newStore(t)
	s := config.Defaults()
	s.Printers = []config.Printer{a1()}
	s.Viewer.OverhangAngle = 50
	if err := st.Save(s); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Load()
	if len(got.Printers) != 1 || got.Printers[0].Config["access_code"] != "12345678" || got.Viewer.OverhangAngle != 50 {
		t.Fatalf("%+v", got)
	}
}

func TestSettingsNeverReturnAccessCode(t *testing.T) {
	st := newStore(t)
	s := config.Defaults()
	s.Printers = []config.Printer{a1()}
	st.Save(s)
	loaded, _ := st.Load()
	masked := loaded.Masked()
	if masked.Printers[0].Config["access_code"] != config.Mask {
		t.Fatalf("not masked: %v", masked.Printers[0].Config)
	}
	if loaded.Printers[0].Config["access_code"] != "12345678" {
		t.Fatal("Masked changed the original")
	}
	// Saving the masked settings back keeps the stored code.
	masked.Printers[0].Name = "Renamed"
	if err := st.Save(masked); err != nil {
		t.Fatal(err)
	}
	again, _ := st.Load()
	if again.Printers[0].Config["access_code"] != "12345678" || again.Printers[0].Name != "Renamed" {
		t.Fatalf("%+v", again.Printers[0])
	}
}

func TestApprovalCannotBeTurnedOff(t *testing.T) {
	s := config.Defaults()
	s.Pipeline.ApprovalRequired = false
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "approval") {
		t.Fatalf("got %v", err)
	}
}

func TestValidationListsEveryProblem(t *testing.T) {
	s := config.Defaults()
	p := a1()
	p.Config["ip"] = "not-an-ip"
	p.Config["serial"] = ""
	q := a1()
	s.Printers = []config.Printer{p, q}
	s.Slicer.Infill = 150
	err := s.Validate()
	var ve *config.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("got %v", err)
	}
	for _, want := range []string{"not an IP", "Serial number is required", "ID used twice", "one printer can be the default", "infill"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
}

func TestUnknownKindRejected(t *testing.T) {
	s := config.Defaults()
	s.Printers = []config.Printer{{ID: "x", Name: "X", Kind: "teleporter"}}
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "unknown printer kind") {
		t.Fatalf("got %v", err)
	}
}

func TestProfilesUploadAndReference(t *testing.T) {
	st := newStore(t)
	if _, err := st.SaveProfile("machine", "../evil.json", []byte(`{}`)); err == nil {
		t.Fatal("path traversal accepted")
	}
	if _, err := st.SaveProfile("filament", "pla.json", []byte(`not json`)); err == nil {
		t.Fatal("non-JSON accepted")
	}
	if _, err := st.SaveProfile("filament", "Bambu PLA Basic.json", []byte(`{"name":"PLA"}`)); err != nil {
		t.Fatal(err)
	}
	s, _ := st.Load()
	p := a1()
	p.Profiles = map[string]string{"filament": "Bambu PLA Basic.json"}
	s.Printers = []config.Printer{p}
	s.Profiles[0].PricePerKg = 25
	if err := st.Save(s); err != nil {
		t.Fatal(err)
	}
	p.Profiles = map[string]string{"machine": "Bambu PLA Basic.json"}
	s.Printers = []config.Printer{p}
	if err := st.Save(s); err == nil || !strings.Contains(err.Error(), "not machine") {
		t.Fatalf("wrong-kind profile accepted: %v", err)
	}
	got, _ := st.Load()
	if paths := st.ProfilePaths(got.Printers[0]); !strings.HasSuffix(paths["filament"], "Bambu PLA Basic.json") {
		t.Fatalf("%v", paths)
	}
}

func TestExportImportMovesTheWholeSetup(t *testing.T) {
	src := newStore(t)
	src.SaveProfile("filament", "pla.json", []byte(`{"name":"PLA"}`))
	s, _ := src.Load()
	p := a1()
	p.Profiles = map[string]string{"filament": "pla.json"}
	s.Printers = []config.Printer{p}
	src.Save(s)
	bundle, err := src.Export()
	if err != nil {
		t.Fatal(err)
	}

	dst := newStore(t)
	if err := dst.Import(bundle); err != nil {
		t.Fatal(err)
	}
	got, _ := dst.Load()
	if len(got.Printers) != 1 || got.Printers[0].Config["access_code"] != "12345678" || len(got.Profiles) != 1 {
		t.Fatalf("%+v", got)
	}
	bundle.Format = "something else"
	if err := dst.Import(bundle); err == nil {
		t.Fatal("wrong format accepted")
	}
}

func TestPrinterLookup(t *testing.T) {
	s := config.Defaults()
	if _, err := s.Printer(""); !errors.Is(err, config.ErrNoPrinter) {
		t.Fatalf("got %v", err)
	}
	f := config.Printer{ID: "sim", Name: "Sim", Kind: "fake"}
	s.Printers = []config.Printer{f, a1()}
	if p, _ := s.Printer(""); p.ID != "a1" {
		t.Fatalf("default: %s", p.ID)
	}
	if p, _ := s.Printer("sim"); p.ID != "sim" {
		t.Fatal("by id")
	}
	if _, err := s.Printer("nope"); err == nil {
		t.Fatal("unknown id found")
	}
}

func TestValidationOfEverySection(t *testing.T) {
	s := config.Defaults()
	s.Slicer.LayerHeight = 0.9
	s.Slicer.SupportType = "glue"
	s.Viewer.OverhangAngle = 95
	s.Viewer.DisplayMode = "hologram"
	s.Pipeline.PreviewAngles = []string{"underneath"}
	s.Pipeline.Workspace = "relative"
	s.Pipeline.LogLevel = "loud"
	s.Currency = "euros"
	s.Profiles = []config.Profile{{Name: "../x.json", Kind: "gizmo", PricePerKg: -1}, {Name: "a.json", Kind: "filament"}, {Name: "a.json", Kind: "filament"}}
	s.Printers = []config.Printer{{ID: "Bad ID", Name: " ", Kind: "fake", Default: true, Config: map[string]string{"print_seconds": "soon", "slicer": "magic"},
		Profiles: map[string]string{"machine": "missing.json", "filament": "a.json", "process": "a.json"}}}
	err := s.Validate()
	for _, want := range []string{"layer height", "support type", "overhang angle", "display mode", "preview angle", "absolute path", "log level", "currency",
		"name must end in .json", "kind must be", "negative", "listed twice", "ID must be", "name is required", "must be a number", "must be one of", "not uploaded", "not process"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q", want)
		}
	}
	s = config.Defaults()
	s.Pipeline.PreviewAngles = nil
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "at least one preview angle") {
		t.Errorf("got %v", err)
	}
}

func TestSavedProfileMustExistOnDisk(t *testing.T) {
	st := newStore(t)
	s := config.Defaults()
	s.Profiles = []config.Profile{{Name: "ghost.json", Kind: "machine"}}
	if err := st.Save(s); err == nil || !strings.Contains(err.Error(), "file not found") {
		t.Fatalf("got %v", err)
	}
	if !strings.HasSuffix(st.ProfilesDir(), "profiles") {
		t.Fatal(st.ProfilesDir())
	}
}

func TestReuploadKeepsPriceAndOnSaveFires(t *testing.T) {
	st := newStore(t)
	saved := 0
	st.OnSave = func(config.Settings) { saved++ }
	st.SaveProfile("filament", "pla.json", []byte(`{"v":1}`))
	s, _ := st.Load()
	s.Profiles[0].PricePerKg = 22
	st.Save(s)
	st.SaveProfile("filament", "pla.json", []byte(`{"v":2}`))
	s, _ = st.Load()
	if len(s.Profiles) != 1 || s.Profiles[0].PricePerKg != 22 || saved != 3 {
		t.Fatalf("%+v saved=%d", s.Profiles, saved)
	}
	if _, err := st.SaveProfile("gizmo", "x.json", []byte(`{}`)); err == nil {
		t.Fatal("unknown kind accepted")
	}
}

func TestImportRejectsBrokenBundles(t *testing.T) {
	st := newStore(t)
	b := config.Bundle{Format: config.BundleFormat, Settings: config.Defaults(), Files: map[string]string{}}
	b.Settings.Profiles = []config.Profile{{Name: "pla.json", Kind: "filament"}}
	if err := st.Import(b); err == nil || !strings.Contains(err.Error(), "file is missing") {
		t.Fatalf("got %v", err)
	}
	b.Files["pla.json"] = "not json"
	if err := st.Import(b); err == nil || !strings.Contains(err.Error(), "not JSON") {
		t.Fatalf("got %v", err)
	}
	b.Settings.Currency = "x"
	if err := st.Import(b); err == nil {
		t.Fatal("invalid settings imported")
	}
}
