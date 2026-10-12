// Package pipeline is the service behind every MCP tool and web endpoint:
// compile → estimate → request approval → (user approves) → print.
package pipeline

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/margarit/printpipe/internal/config"
	"github.com/margarit/printpipe/internal/estimates"
	"github.com/margarit/printpipe/internal/events"
	"github.com/margarit/printpipe/internal/jobs"
	"github.com/margarit/printpipe/internal/mesh"
	"github.com/margarit/printpipe/internal/printer"
	"github.com/margarit/printpipe/internal/project"
	"github.com/margarit/printpipe/internal/runner"
	"github.com/margarit/printpipe/internal/scad"
	"github.com/margarit/printpipe/internal/slicer"
)

// Publisher sends events to the viewer.
type Publisher interface {
	Publish(typ string, v any)
}

type Deps struct {
	DB       *sql.DB
	Runner   runner.Runner
	Config   *config.Store
	Jobs     *jobs.Store
	Projects *project.Store
	SCAD     *scad.Compiler
	Slicers  slicer.Set
	Events   Publisher
	// NewPrinter builds a configured printer; nil uses the driver registry.
	NewPrinter func(config.Printer) (printer.Printer, error)
	Logger     *slog.Logger
	// ViewerURL is where the user watches and approves, for messages to the agent.
	ViewerURL string
	// PollEvery is how often printer status is read during a print.
	PollEvery time.Duration
}

type Service struct {
	d   Deps
	est *estimates.Service

	watchMu  sync.Mutex
	watching map[string]context.CancelFunc
}

func New(d Deps) *Service {
	if d.Logger == nil {
		d.Logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	if d.ViewerURL == "" {
		d.ViewerURL = "http://localhost:8080"
	}
	if d.PollEvery == 0 {
		d.PollEvery = 5 * time.Second
	}
	if d.NewPrinter == nil {
		runner := d.Runner
		d.NewPrinter = func(p config.Printer) (printer.Printer, error) {
			drv, ok := printer.Lookup(p.Kind)
			if !ok {
				return nil, fmt.Errorf("printer %q: unknown kind %q", p.ID, p.Kind)
			}
			return drv.New(p.Config, printer.Deps{Runner: runner})
		}
	}
	s := &Service{d: d, watching: map[string]context.CancelFunc{}}
	s.est = estimates.New(d.DB, func(e estimates.Estimate) { d.Events.Publish(events.Estimate, e) })
	return s
}

// Errors the agent and the API see.
var (
	ErrTooBig      = errors.New("the part does not fit the printer's build volume")
	ErrWrongFormat = errors.New("the sliced file is not in the format this printer accepts")
)

// ---- run log -------------------------------------------------------------

// record runs fn as one tool call, logging it to the runs table, the project's run.log and stderr.
func (s *Service) record(project, tool string, args any, fn func() error) error {
	start := time.Now().UTC()
	rawArgs, _ := json.Marshal(args)
	res, _ := s.d.DB.Exec(`INSERT INTO runs (project, tool, args, started_at) VALUES (?, ?, ?, ?)`,
		nullable(project), tool, string(rawArgs), start.Format(time.RFC3339Nano))
	err := fn()
	code, msg := 0, "ok"
	if err != nil {
		code, msg = 1, err.Error()
	}
	if res != nil {
		if id, e := res.LastInsertId(); e == nil {
			s.d.DB.Exec(`UPDATE runs SET exit_code = ?, message = ?, finished_at = ?, updated_at = ? WHERE id = ?`,
				code, msg, time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano), id)
		}
	}
	took := time.Since(start).Round(time.Millisecond)
	if project != "" {
		first, _, _ := strings.Cut(msg, "\n")
		s.d.Projects.Log(project, fmt.Sprintf("%s exit=%d %s (%s)", tool, code, first, took))
	}
	if err != nil {
		s.d.Logger.Warn("tool failed", "tool", tool, "project", project, "took", took, "err", err)
	} else {
		s.d.Logger.Info("tool ok", "tool", tool, "project", project, "took", took)
	}
	return err
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

type Run struct {
	Tool       string    `json:"tool"`
	Args       string    `json:"args"`
	ExitCode   *int      `json:"exit_code"`
	Message    string    `json:"message"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
}

// Runs lists the latest tool calls for a project (all projects when empty), newest first.
func (s *Service) Runs(proj string, limit int) ([]Run, error) {
	q := `SELECT tool, args, exit_code, coalesce(message, ''), started_at, coalesce(finished_at, '') FROM runs`
	args := []any{}
	if proj != "" {
		q += ` WHERE project = ?`
		args = append(args, proj)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.d.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		var r Run
		var code sql.NullInt64
		var started, finished string
		if err := rows.Scan(&r.Tool, &r.Args, &code, &r.Message, &started, &finished); err != nil {
			return nil, err
		}
		if code.Valid {
			c := int(code.Int64)
			r.ExitCode = &c
		}
		r.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
		r.FinishedAt, _ = time.Parse(time.RFC3339Nano, finished)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---- compile -------------------------------------------------------------

type Image struct {
	Angle string `json:"angle"`
	PNG   []byte `json:"-"`
}

type CompileResult struct {
	Project      string              `json:"project"`
	Version      int                 `json:"version"`
	Size         mesh.Vec            `json:"size_mm"`
	PrinterID    string              `json:"printer_id,omitempty"`
	PrinterModel string              `json:"printer_model,omitempty"`
	BuildVolume  printer.Volume      `json:"build_volume,omitempty"`
	Fits         bool                `json:"fits"`
	Params       map[string]any      `json:"params"`
	Images       []Image             `json:"-"`
	Warnings     []string            `json:"warnings,omitempty"`
	Estimate     *estimates.Estimate `json:"estimate,omitempty"`
	ViewerURL    string              `json:"viewer_url"`
}

// Summary is the text the agent reads after a compile.
func (r CompileResult) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Compiled %s version %d. Size %.1f × %.1f × %.1f mm (W × D × H).", r.Project, r.Version, r.Size.X, r.Size.Y, r.Size.Z)
	if r.PrinterModel != "" {
		if r.Fits {
			fmt.Fprintf(&b, " Fits the %s (%g × %g × %g mm).", r.PrinterModel, r.BuildVolume.X, r.BuildVolume.Y, r.BuildVolume.Z)
		} else {
			fmt.Fprintf(&b, " DOES NOT FIT the %s (%g × %g × %g mm): make it smaller before slicing.", r.PrinterModel, r.BuildVolume.X, r.BuildVolume.Y, r.BuildVolume.Z)
		}
	}
	if len(r.Params) > 0 {
		keys := make([]string, 0, len(r.Params))
		for k := range r.Params {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = fmt.Sprintf("%s=%v", k, fmtValue(r.Params[k]))
		}
		fmt.Fprintf(&b, "\nParameters: %s.", strings.Join(parts, ", "))
	}
	if r.Estimate != nil {
		fmt.Fprintf(&b, "\nEstimate: %s.", r.Estimate.Line())
	}
	for _, w := range r.Warnings {
		fmt.Fprintf(&b, "\nWarning: %s", w)
	}
	if len(r.Images) > 0 {
		angles := make([]string, len(r.Images))
		for i, im := range r.Images {
			angles[i] = im.Angle
		}
		fmt.Fprintf(&b, "\nPreview images attached (%s). Check them for wrong geometry.", strings.Join(angles, ", "))
	}
	fmt.Fprintf(&b, "\nThe user sees it live at %s.", r.ViewerURL)
	return b.String()
}

func fmtValue(v any) string {
	if s, ok := v.(string); ok {
		return fmt.Sprintf("%q", s)
	}
	return fmt.Sprint(v)
}

// Compile writes model.scad as a new version of the project, compiles it to STL, renders
// the preview images and tells the viewer. Empty code recompiles the latest version's
// code with new parameter values.
func (s *Service) Compile(ctx context.Context, proj, code string, params map[string]any) (CompileResult, error) {
	var res CompileResult
	err := s.record(proj, "compile_model", map[string]any{"params": params, "code_bytes": len(code)}, func() error {
		var err error
		res, err = s.compile(ctx, proj, code, params)
		return err
	})
	return res, err
}

func (s *Service) compile(ctx context.Context, proj, code string, params map[string]any) (CompileResult, error) {
	if err := project.ValidName(proj); err != nil {
		return CompileResult{}, err
	}
	set, err := s.d.Config.Load()
	if err != nil {
		return CompileResult{}, err
	}
	if strings.TrimSpace(code) == "" {
		latest, err := s.d.Projects.Get(proj, 0)
		if err != nil {
			return CompileResult{}, fmt.Errorf("no scad_code given and %w", err)
		}
		b, err := os.ReadFile(latest.Path(project.SCAD))
		if err != nil {
			return CompileResult{}, err
		}
		code = string(b)
	}
	decl := scad.ParseParams(code)
	defines, err := scad.Defines(decl, params)
	if err != nil {
		return CompileResult{}, err
	}

	v, err := s.d.Projects.Next(proj)
	if err != nil {
		return CompileResult{}, err
	}
	ok := false
	defer func() {
		if !ok {
			s.d.Projects.Discard(v)
		}
	}()
	if err := os.WriteFile(v.Path(project.SCAD), []byte(code), 0o644); err != nil {
		return CompileResult{}, err
	}
	m, err := s.d.SCAD.Compile(ctx, v.Path(project.SCAD), v.Path(project.STL), defines)
	if err != nil {
		return CompileResult{}, err
	}
	v.STLHash, err = jobs.HashFile(v.Path(project.STL))
	if err != nil {
		return CompileResult{}, err
	}
	v.Params = scad.Values(decl, params)

	res := CompileResult{Project: proj, Size: m.Size(), Params: v.Params, ViewerURL: s.d.ViewerURL}
	for i, angle := range set.Pipeline.PreviewAngles {
		png := v.Path("preview-" + angle + ".png")
		if err := s.d.SCAD.Render(ctx, v.Path(project.STL), png, angle); err != nil {
			res.Warnings = append(res.Warnings, err.Error())
			continue
		}
		b, err := os.ReadFile(png)
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("the %s preview was not written", angle))
			continue
		}
		res.Images = append(res.Images, Image{Angle: angle, PNG: b})
		if i == 0 || len(res.Images) == 1 {
			os.WriteFile(v.Path(project.PNG), b, 0o644)
		}
	}

	v, err = s.d.Projects.Commit(v)
	if err != nil {
		return CompileResult{}, err
	}
	ok = true
	res.Version = v.Number

	if cp, err := set.Printer(""); err == nil {
		if p, err := s.d.NewPrinter(cp); err == nil {
			caps := p.Capabilities()
			res.PrinterID, res.PrinterModel, res.BuildVolume = cp.ID, caps.Model, caps.BuildVolume
			res.Fits = printer.Fits(caps, toVolume(res.Size))
		}
	}
	s.d.Events.Publish(events.Model, map[string]any{"project": proj, "version": v.Number, "size_mm": res.Size, "fits": res.Fits})
	return res, nil
}

func toVolume(v mesh.Vec) printer.Volume { return printer.Volume{X: v.X, Y: v.Y, Z: v.Z} }

// ---- printers ------------------------------------------------------------

type PrinterInfo struct {
	ID           string               `json:"id"`
	Name         string               `json:"name"`
	Kind         string               `json:"kind"`
	Default      bool                 `json:"default"`
	Capabilities printer.Capabilities `json:"capabilities"`
	Profiles     map[string]string    `json:"profiles"`
	Status       *printer.Status      `json:"status,omitempty"`
	Error        string               `json:"error,omitempty"`
}

// Printers lists the configured printers; withStatus also asks each one for its state.
func (s *Service) Printers(ctx context.Context, withStatus bool) ([]PrinterInfo, error) {
	set, err := s.d.Config.Load()
	if err != nil {
		return nil, err
	}
	def, _ := set.Default()
	out := []PrinterInfo{}
	for _, cp := range set.Printers {
		info := PrinterInfo{ID: cp.ID, Name: cp.Name, Kind: cp.Kind, Default: cp.ID == def.ID, Profiles: cp.Profiles}
		p, err := s.d.NewPrinter(cp)
		if err != nil {
			info.Error = err.Error()
			out = append(out, info)
			continue
		}
		info.Capabilities = p.Capabilities()
		if withStatus && info.Capabilities.Has(printer.FeatureStatus) {
			sctx, cancel := context.WithTimeout(ctx, 8*time.Second)
			st, err := p.Status(sctx)
			cancel()
			if err != nil {
				info.Error = err.Error()
			} else {
				info.Status = &st
			}
		}
		out = append(out, info)
	}
	return out, nil
}

func (s *Service) printerFor(set config.Settings, id string) (config.Printer, printer.Printer, error) {
	cp, err := set.Printer(id)
	if err != nil {
		return cp, nil, err
	}
	p, err := s.d.NewPrinter(cp)
	if err != nil {
		return cp, nil, fmt.Errorf("printer %q: %w", cp.ID, err)
	}
	return cp, p, nil
}

// TestPrinter runs a printer's connection test, step by step when the driver supports it.
func (s *Service) TestPrinter(ctx context.Context, id string) ([]printer.Check, error) {
	var checks []printer.Check
	err := s.record("", "test_printer", map[string]any{"printer_id": id}, func() error {
		set, err := s.d.Config.Load()
		if err != nil {
			return err
		}
		_, p, err := s.printerFor(set, id)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if d, ok := p.(printer.Diagnoser); ok {
			checks = d.Diagnose(ctx)
			return nil
		}
		if err := p.Test(ctx); err != nil {
			checks = []printer.Check{{Name: "Connection", Message: err.Error()}}
		} else {
			checks = []printer.Check{{Name: "Connection", OK: true, Message: "ok"}}
		}
		return nil
	})
	return checks, err
}

// Status reads a printer's state (the default printer when id is empty).
func (s *Service) Status(ctx context.Context, id string) (printer.Status, error) {
	set, err := s.d.Config.Load()
	if err != nil {
		return printer.Status{}, err
	}
	_, p, err := s.printerFor(set, id)
	if err != nil {
		return printer.Status{}, err
	}
	return p.Status(ctx)
}

// Cancel stops the current print on a printer.
func (s *Service) Cancel(ctx context.Context, id string) error {
	return s.record("", "cancel_print", map[string]any{"printer_id": id}, func() error {
		set, err := s.d.Config.Load()
		if err != nil {
			return err
		}
		cp, p, err := s.printerFor(set, id)
		if err != nil {
			return err
		}
		if err := p.Cancel(ctx); err != nil {
			return err
		}
		if st, err := p.Status(ctx); err == nil {
			s.d.Events.Publish(events.Printer, map[string]any{"printer_id": cp.ID, "status": st})
		}
		return nil
	})
}

// ---- estimates -----------------------------------------------------------

// estimateInput builds the cache key and slice function for a version on a printer.
func (s *Service) estimateInput(set config.Settings, cp config.Printer, p printer.Printer, v project.Version, o slicer.Overrides) (estimates.Input, estimates.SliceFunc, error) {
	caps := p.Capabilities()
	m, err := mesh.ReadFile(v.Path(project.STL))
	if err != nil {
		return estimates.Input{}, nil, fmt.Errorf("version %d: %w", v.Number, err)
	}
	size := m.Size()
	if !printer.Fits(caps, toVolume(size)) {
		return estimates.Input{}, nil, fmt.Errorf("%w: the part is %.1f × %.1f × %.1f mm, the %s holds %g × %g × %g mm",
			ErrTooBig, size.X, size.Y, size.Z, caps.Model, caps.BuildVolume.X, caps.BuildVolume.Y, caps.BuildVolume.Z)
	}
	sl, err := s.d.Slicers.Get(caps.Slicer)
	if err != nil {
		return estimates.Input{}, nil, err
	}
	o = mergeOverrides(set.Slicer, o)
	profiles := s.d.Config.ProfilePaths(cp)

	h := sha256.New()
	fmt.Fprintf(h, "slicer=%s\n", caps.Slicer)
	kinds := make([]string, 0, len(profiles))
	for k := range profiles {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		b, err := os.ReadFile(profiles[k])
		if err != nil {
			return estimates.Input{}, nil, fmt.Errorf("%s profile: %w", k, err)
		}
		fmt.Fprintf(h, "%s=%x\n", k, sha256.Sum256(b))
	}
	ob, _ := json.Marshal(o)
	h.Write(ob)

	price := 0.0
	if prof, ok := set.Profile(cp.Profiles[slicer.ProfileFilament]); ok {
		price = prof.PricePerKg
	}
	in := estimates.Input{Project: v.Project, Version: v.Number, STLHash: v.STLHash, PrinterID: cp.ID,
		ProfileHashes: hex.EncodeToString(h.Sum(nil)), PricePerKg: price, Currency: set.Currency}
	out := filepath.Join(v.Dir, "slices", in.Key()[:12], project.Job)
	slice := func(ctx context.Context) (slicer.Result, error) {
		return sl.Slice(ctx, slicer.Input{Model: v.Path(project.STL), OutPath: out, Profiles: profiles, Overrides: o})
	}
	return in, slice, nil
}

// mergeOverrides applies the settings page defaults under the job's own overrides.
func mergeOverrides(d config.SlicerDefault, o slicer.Overrides) slicer.Overrides {
	if o.Supports == nil {
		on := d.Supports
		o.Supports = &on
	}
	if o.SupportType == "" {
		o.SupportType = d.SupportType
	}
	if o.LayerHeight == 0 {
		o.LayerHeight = d.LayerHeight
	}
	if o.Infill == nil && d.Infill >= 0 {
		v := d.Infill
		o.Infill = &v
	}
	return o
}

// Estimate slices versions of a project (the latest when versions is empty) for a printer
// and returns time, filament and cost. It never creates or approves a job.
func (s *Service) Estimate(ctx context.Context, proj string, versions []int, printerID string, o slicer.Overrides) ([]estimates.Estimate, error) {
	var out []estimates.Estimate
	err := s.record(proj, "estimate_print", map[string]any{"versions": versions, "printer_id": printerID, "overrides": o}, func() error {
		set, err := s.d.Config.Load()
		if err != nil {
			return err
		}
		cp, p, err := s.printerFor(set, printerID)
		if err != nil {
			return err
		}
		if len(versions) == 0 {
			versions = []int{0}
		}
		var failures []string
		for _, n := range versions {
			v, err := s.d.Projects.Get(proj, n)
			if err != nil {
				return err
			}
			e, err := s.estimateVersion(ctx, set, cp, p, v, o)
			if err != nil {
				failures = append(failures, fmt.Sprintf("version %d: %v", v.Number, err))
				out = append(out, estimates.Estimate{Project: proj, Version: v.Number, PrinterID: cp.ID, Status: "failed", Error: err.Error()})
				continue
			}
			out = append(out, e)
		}
		if len(failures) == len(versions) {
			return errors.New(strings.Join(failures, "\n"))
		}
		return nil
	})
	return out, err
}

func (s *Service) estimateVersion(ctx context.Context, set config.Settings, cp config.Printer, p printer.Printer, v project.Version, o slicer.Overrides) (estimates.Estimate, error) {
	in, slice, err := s.estimateInput(set, cp, p, v, o)
	if err != nil {
		return estimates.Estimate{}, err
	}
	e, err := s.est.Get(ctx, in, slice)
	if err != nil {
		return e, err
	}
	s.d.Events.Publish(events.Estimate, e)
	return e, nil
}

// StartEstimate returns a cached estimate at once, or starts one in the background; the
// result arrives as an estimate event (PRD FR-35).
func (s *Service) StartEstimate(proj string, version int, printerID string) (estimates.Estimate, error) {
	set, err := s.d.Config.Load()
	if err != nil {
		return estimates.Estimate{}, err
	}
	cp, p, err := s.printerFor(set, printerID)
	if err != nil {
		return estimates.Estimate{}, err
	}
	v, err := s.d.Projects.Get(proj, version)
	if err != nil {
		return estimates.Estimate{}, err
	}
	in, slice, err := s.estimateInput(set, cp, p, v, slicer.Overrides{})
	if err != nil {
		return estimates.Estimate{}, err
	}
	return s.est.Start(in, slice)
}

// CachedEstimate returns the stored estimate for a version on a printer, if any.
func (s *Service) CachedEstimate(proj string, version int, printerID string) (estimates.Estimate, bool, error) {
	set, err := s.d.Config.Load()
	if err != nil {
		return estimates.Estimate{}, false, err
	}
	cp, p, err := s.printerFor(set, printerID)
	if err != nil {
		return estimates.Estimate{}, false, err
	}
	v, err := s.d.Projects.Get(proj, version)
	if err != nil {
		return estimates.Estimate{}, false, err
	}
	in, _, err := s.estimateInput(set, cp, p, v, slicer.Overrides{})
	if err != nil {
		return estimates.Estimate{}, false, err
	}
	return s.est.Cached(in)
}

// ---- projects ------------------------------------------------------------

type VersionInfo struct {
	Version   int                  `json:"version"`
	Params    map[string]any       `json:"params"`
	CreatedAt time.Time            `json:"created_at"`
	Size      *mesh.Vec            `json:"size_mm,omitempty"`
	Estimates []estimates.Estimate `json:"estimates"`
}

func (s *Service) Projects() ([]project.Summary, error) { return s.d.Projects.Projects() }

// Versions lists every version of a project with any cached estimates (PRD V-12, FR-37).
func (s *Service) Versions(proj string) ([]VersionInfo, error) {
	set, err := s.d.Config.Load()
	if err != nil {
		return nil, err
	}
	vs, err := s.d.Projects.List(proj)
	if err != nil {
		return nil, err
	}
	price := func(printerID string) (float64, string) {
		cp, err := set.Printer(printerID)
		if err != nil {
			return 0, ""
		}
		if prof, ok := set.Profile(cp.Profiles[slicer.ProfileFilament]); ok {
			return prof.PricePerKg, set.Currency
		}
		return 0, ""
	}
	out := []VersionInfo{}
	for _, v := range vs {
		info := VersionInfo{Version: v.Number, Params: v.Params, CreatedAt: v.CreatedAt}
		if m, err := mesh.ReadFile(v.Path(project.STL)); err == nil {
			sz := m.Size()
			info.Size = &sz
		}
		info.Estimates, err = s.est.ForVersion(proj, v.Number, price)
		if err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	return out, nil
}

// Restore makes an old version current again by compiling its code and parameters as a new version (PRD V-12).
func (s *Service) Restore(ctx context.Context, proj string, n int) (CompileResult, error) {
	v, err := s.d.Projects.Get(proj, n)
	if err != nil {
		return CompileResult{}, err
	}
	code, err := os.ReadFile(v.Path(project.SCAD))
	if err != nil {
		return CompileResult{}, err
	}
	params := map[string]any{}
	for _, p := range scad.ParseParams(string(code)) {
		if val, ok := v.Params[p.Name]; ok && fmt.Sprint(val) != fmt.Sprint(p.Value) {
			params[p.Name] = val
		}
	}
	var res CompileResult
	err = s.record(proj, "restore_version", map[string]any{"version": v.Number}, func() error {
		var err error
		res, err = s.compile(ctx, proj, string(code), params)
		return err
	})
	return res, err
}

// Version returns one version (0 = latest).
func (s *Service) Version(proj string, n int) (project.Version, error) {
	return s.d.Projects.Get(proj, n)
}

// Params returns the model's top-level parameters with the version's values (0 = latest).
func (s *Service) Params(proj string, n int) ([]scad.Param, error) {
	v, err := s.d.Projects.Get(proj, n)
	if err != nil {
		return nil, err
	}
	code, err := os.ReadFile(v.Path(project.SCAD))
	if err != nil {
		return nil, err
	}
	params := scad.ParseParams(string(code))
	for i, p := range params {
		if val, ok := v.Params[p.Name]; ok {
			params[i].Value = val
		}
	}
	return params, nil
}

// ---- approval and printing -----------------------------------------------

type Approval struct {
	Job         jobs.Job           `json:"job"`
	Estimate    estimates.Estimate `json:"estimate"`
	PrinterName string             `json:"printer_name"`
	ViewerURL   string             `json:"viewer_url"`
}

// RequestApproval slices a version for a printer (reusing a cached slice) and records a job
// that waits for the user's Approve click. Nothing is sent to the printer.
func (s *Service) RequestApproval(ctx context.Context, proj string, version int, printerID string, o slicer.Overrides) (Approval, error) {
	var a Approval
	err := s.record(proj, "request_approval", map[string]any{"version": version, "printer_id": printerID, "overrides": o}, func() error {
		set, err := s.d.Config.Load()
		if err != nil {
			return err
		}
		cp, p, err := s.printerFor(set, printerID)
		if err != nil {
			return err
		}
		v, err := s.d.Projects.Get(proj, version)
		if err != nil {
			return err
		}
		e, err := s.estimateVersion(ctx, set, cp, p, v, o)
		if err != nil {
			return err
		}
		if !strings.HasSuffix(e.JobPath, p.Capabilities().JobFormat) {
			return fmt.Errorf("%w (%s)", ErrWrongFormat, p.Capabilities().JobFormat)
		}
		job, err := s.d.Jobs.RequestVersion(proj, v.Number, cp.ID, e.JobPath, e)
		if err != nil {
			return err
		}
		s.d.Projects.Promote(v, e.JobPath, project.Job)
		a = Approval{Job: job, Estimate: e, PrinterName: cp.Name, ViewerURL: s.d.ViewerURL}
		s.d.Events.Publish(events.Job, a)
		return nil
	})
	return a, err
}

// Approve marks a job printable. Only the web API calls it, after the user clicks Approve.
func (s *Service) Approve(jobID string) (jobs.Job, error) {
	job, getErr := s.d.Jobs.Get(jobID)
	err := s.record(job.Project, "approve", map[string]any{"job_id": jobID, "by": "user"}, func() error {
		if getErr != nil {
			return getErr
		}
		if job.Printed {
			return jobs.ErrPrinted
		}
		if err := s.d.Jobs.Approve(jobID); err != nil {
			return err
		}
		var err error
		job, err = s.d.Jobs.Get(jobID)
		if err != nil {
			return err
		}
		s.d.Events.Publish(events.Job, map[string]any{"job": job})
		return nil
	})
	return job, err
}

// PendingJobs lists the project's jobs that were not printed yet.
func (s *Service) PendingJobs(proj string) ([]jobs.Job, error) { return s.d.Jobs.Pending(proj) }

func (s *Service) Job(id string) (jobs.Job, error) { return s.d.Jobs.Get(id) }

type PrintResult struct {
	Job     jobs.Job       `json:"job"`
	Printer string         `json:"printer"`
	Remote  string         `json:"remote_file"`
	Status  printer.Status `json:"status"`
}

// Print sends an approved job to its printer and starts it. The approval is checked
// before the printer is contacted at all.
func (s *Service) Print(ctx context.Context, jobID string) (PrintResult, error) {
	var res PrintResult
	job, getErr := s.d.Jobs.Get(jobID)
	err := s.record(job.Project, "print_job", map[string]any{"job_id": jobID}, func() error {
		if getErr != nil {
			return getErr
		}
		if err := s.d.Jobs.CheckPrintable(job.ID, job.FilePath); err != nil {
			return err
		}
		set, err := s.d.Config.Load()
		if err != nil {
			return err
		}
		cp, err := set.Printer(job.PrinterID)
		if err != nil {
			return fmt.Errorf("the job's printer is no longer configured: %w", err)
		}
		p, err := s.d.NewPrinter(cp)
		if err != nil {
			return err
		}
		caps := p.Capabilities()
		if !strings.HasSuffix(job.FilePath, caps.JobFormat) {
			return fmt.Errorf("%w (%s)", ErrWrongFormat, caps.JobFormat)
		}
		// Pre-flight: don't upload to a printer that is offline or busy.
		if caps.Has(printer.FeatureStatus) {
			st, err := p.Status(ctx)
			if err != nil {
				return fmt.Errorf("pre-flight check: %w", err)
			}
			if st.State == printer.Printing || st.State == printer.Paused {
				return fmt.Errorf("pre-flight check: %w (currently %s)", printer.ErrBusy, st.State)
			}
		}
		name := fmt.Sprintf("%s_v%d%s", job.Project, job.Version, caps.JobFormat)
		remote, err := p.Upload(ctx, printer.Job{Path: job.FilePath, Name: name})
		if err != nil {
			return fmt.Errorf("upload to %s: %w", cp.Name, err)
		}
		if err := p.Start(ctx, remote); err != nil {
			return fmt.Errorf("start on %s: %w", cp.Name, err)
		}
		if err := s.d.Jobs.MarkPrinted(job.ID); err != nil {
			return err
		}
		res = PrintResult{Job: job, Printer: cp.Name, Remote: remote.ID, Status: printer.Status{State: printer.Printing}}
		res.Job.Printed = true
		if caps.Has(printer.FeatureStatus) {
			if st, err := p.Status(ctx); err == nil {
				res.Status = st
			}
			s.watch(cp, p)
		}
		s.d.Events.Publish(events.Printer, map[string]any{"printer_id": cp.ID, "status": res.Status, "job": res.Job})
		return nil
	})
	return res, err
}

// watch polls a printer's status during a print and publishes it to the viewer (PRD FR-14, V-14).
func (s *Service) watch(cp config.Printer, p printer.Printer) {
	s.watchMu.Lock()
	if cancel, ok := s.watching[cp.ID]; ok {
		cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 48*time.Hour)
	s.watching[cp.ID] = cancel
	s.watchMu.Unlock()
	go func() {
		defer cancel()
		t := time.NewTicker(s.d.PollEvery)
		defer t.Stop()
		misses := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			sctx, c := context.WithTimeout(ctx, 15*time.Second)
			st, err := p.Status(sctx)
			c()
			if err != nil {
				misses++
				s.d.Events.Publish(events.Printer, map[string]any{"printer_id": cp.ID, "status": printer.Status{State: printer.Offline, Message: err.Error()}})
				if misses >= 12 {
					return
				}
				continue
			}
			misses = 0
			s.d.Events.Publish(events.Printer, map[string]any{"printer_id": cp.ID, "status": st})
			if st.State != printer.Printing && st.State != printer.Paused {
				return
			}
		}
	}()
}

// Close stops background status polling.
func (s *Service) Close() {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	for _, cancel := range s.watching {
		cancel()
	}
}
