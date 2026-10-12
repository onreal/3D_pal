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
	"strings"

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
}

// New returns the slicer; binary is the Bambu Studio executable ($BAMBU_STUDIO in the image).
func New(r runner.Runner, binary string) *Slicer {
	if binary == "" {
		binary = "bambu-studio"
	}
	return &Slicer{run: r, binary: binary}
}

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

var ErrNoProfiles = errors.New("slicer profiles missing: upload the machine, process and filament profiles on the settings page (http://localhost:8080/settings)")

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
	process, err := WriteProcess(in.Profiles[slicer.ProfileProcess], filepath.Join(dir, "process.json"), in.Overrides)
	if err != nil {
		return slicer.Result{}, err
	}
	os.Remove(in.OutPath)
	args := Args(in.Profiles[slicer.ProfileMachine], process, in.Profiles[slicer.ProfileFilament], in.Model, in.OutPath)
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

// WriteProcess copies the process profile to dst with the job's overrides applied and
// returns dst. Without overrides it returns src unchanged.
func WriteProcess(src, dst string, o slicer.Overrides) (string, error) {
	if o.Supports == nil && o.SupportType == "" && o.LayerHeight == 0 && o.Infill == nil {
		return src, nil
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	var p map[string]any
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("process profile is not JSON: %w", err)
	}
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
	if name, ok := p["name"].(string); ok && !strings.HasSuffix(name, " (job)") {
		p["name"] = name + " (job)"
	}
	out, _ := json.MarshalIndent(p, "", "    ")
	return dst, os.WriteFile(dst, out, 0o644)
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
