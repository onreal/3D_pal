// Package bambustudio slices with the Bambu Studio command line (PRD FR-8 to FR-10).
package bambustudio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/margarit/printpipe/internal/runner"
	"github.com/margarit/printpipe/internal/slicer"
)

const Kind = "bambustudio"

func init() {
	slicer.Register(slicer.Driver{Kind: Kind, Label: "Bambu Studio", New: func(d slicer.Deps) slicer.Slicer {
		return New(d.Runner, os.Getenv("BAMBU_STUDIO"))
	}})
}

type Slicer struct {
	run    runner.Runner
	binary string
	lib    *Library
}

// New returns the slicer; binary is the Bambu Studio executable ($BAMBU_STUDIO in the image).
// Its system presets are read from $BAMBU_PROFILES, or resources/profiles/BBL next to it.
func New(r runner.Runner, binary string) *Slicer {
	if binary == "" {
		binary = "bambu-studio"
	}
	root := os.Getenv("BAMBU_PROFILES")
	if root == "" {
		root = filepath.Join(filepath.Dir(binary), "resources", "profiles", "BBL")
	}
	return &Slicer{run: r, binary: binary, lib: NewLibrary(root)}
}

// WithLibrary replaces the system preset library (tests).
func (s *Slicer) WithLibrary(l *Library) *Slicer { s.lib = l; return s }

func (s *Slicer) Kind() string { return Kind }

// Args is the exact command line: auto-orient, arrange, the configured profiles, slice all plates, export.
func Args(machine, process, filament, model, out string) []string {
	return []string{
		"--orient", "1", "--arrange", "1",
		"--load-settings", machine + ";" + process,
		"--load-filaments", filament,
		"--slice", "0",
		"--outputdir", filepath.Dir(out),
		"--export-3mf", filepath.Base(out),
		model,
	}
}

var ErrNoProfiles = errors.New("slicer profiles missing and Bambu Studio's system presets are not installed: upload the machine, process and filament profiles on the settings page (http://localhost:8080/settings)")

func (s *Slicer) Slice(ctx context.Context, in slicer.Input) (slicer.Result, error) {
	for _, k := range []string{slicer.ProfileMachine, slicer.ProfileProcess, slicer.ProfileFilament} {
		p := in.Profiles[k]
		if p == "" {
			return slicer.Result{}, ErrNoProfiles
		}
		if _, err := os.Stat(p); err != nil {
			return slicer.Result{}, fmt.Errorf("%s profile %s: %w", k, p, err)
		}
	}
	dir := filepath.Dir(in.OutPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return slicer.Result{}, err
	}
	// The CLI gets self-contained presets: the inherits chain flattened, the job's overrides applied.
	flat := map[string]string{}
	model := "" // the printer model, from the machine preset, for its default plate
	if mm, err := s.lib.Flatten(slicer.ProfileMachine, in.Profiles[slicer.ProfileMachine]); err == nil {
		model, _ = mm["printer_model"].(string)
	}
	for _, k := range []string{slicer.ProfileMachine, slicer.ProfileProcess, slicer.ProfileFilament} {
		m, err := s.lib.Flatten(k, in.Profiles[k])
		if err != nil {
			return slicer.Result{}, err
		}
		if k == slicer.ProfileProcess {
			ApplyOverrides(m, in.Overrides)
			// The command line slices for a Cool Plate (35 °C bed for PLA) unless told the
			// plate: use the one set on the printer, or the one the model ships with.
			if _, set := m["curr_bed_type"]; !set {
				if bt := s.lib.DefaultBedType(model); bt != "" {
					m["curr_bed_type"] = bt
				}
			}
		}
		flat[k] = filepath.Join(dir, k+".json")
		b, _ := json.MarshalIndent(m, "", "    ")
		if err := os.WriteFile(flat[k], b, 0o644); err != nil {
			return slicer.Result{}, err
		}
	}
	os.Remove(in.OutPath)
	os.Remove(filepath.Join(dir, "result.json"))
	args := Args(flat[slicer.ProfileMachine], flat[slicer.ProfileProcess], flat[slicer.ProfileFilament], in.Model, in.OutPath)
	if _, err := s.run.Run(ctx, s.binary, args...); err != nil {
		if msg := resultMessage(dir); msg != "" {
			return slicer.Result{}, fmt.Errorf("Bambu Studio could not slice: %s", msg)
		}
		return slicer.Result{}, err
	}
	if _, err := os.Stat(in.OutPath); err != nil {
		if msg := resultMessage(dir); msg != "" {
			return slicer.Result{}, fmt.Errorf("Bambu Studio could not slice: %s", msg)
		}
		return slicer.Result{}, fmt.Errorf("Bambu Studio finished but wrote no %s", filepath.Base(in.OutPath))
	}
	res, err := ReadResult(in.OutPath)
	if err != nil {
		return res, err
	}
	if res.OffPlate {
		return res, errors.New("the part does not fit on the plate after arranging")
	}
	return res, nil
}

// resultMessage reads result.json, where the CLI explains a failed slice.
func resultMessage(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "result.json"))
	if err != nil {
		return ""
	}
	var r struct {
		ReturnCode  int    `json:"return_code"`
		ErrorString string `json:"error_string"`
	}
	if json.Unmarshal(b, &r) != nil || r.ReturnCode == 0 {
		return ""
	}
	return fmt.Sprintf("%s (code %d)", r.ErrorString, r.ReturnCode)
}

// ApplyOverrides changes a process preset for one job; zero overrides leave it unchanged.
func ApplyOverrides(p map[string]any, o slicer.Overrides) {
	if o.Supports != nil {
		p["enable_support"] = boolStr(*o.Supports)
	}
	if o.SupportType != "" {
		p["support_type"] = o.SupportType
	}
	if o.LayerHeight > 0 {
		p["layer_height"] = strconv.FormatFloat(o.LayerHeight, 'f', -1, 64)
	}
	if o.Infill != nil {
		p["sparse_infill_density"] = strconv.Itoa(*o.Infill) + "%"
	}
	if o.BedType != "" {
		p["curr_bed_type"] = o.BedType
	}
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
