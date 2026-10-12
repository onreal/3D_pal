// Package config loads, validates and saves every setting (PRD FR-15 to FR-20). Settings
// live in SQLite; uploaded slicer profiles are files in the profiles directory.
package config

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/margarit/printpipe/internal/printer"
	"github.com/margarit/printpipe/internal/slicer"
)

// Mask stands in for a stored secret on the settings page; saving it back keeps the secret.
const Mask = "••••••••"

type Settings struct {
	Printers []Printer     `json:"printers"`
	Profiles []Profile     `json:"profiles"`
	Slicer   SlicerDefault `json:"slicer"`
	Viewer   Viewer        `json:"viewer"`
	Pipeline Pipeline      `json:"pipeline"`
	Currency string        `json:"currency"`
}

type Printer struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Kind     string            `json:"kind"`
	Default  bool              `json:"default"`
	Config   map[string]string `json:"config"`
	Profiles map[string]string `json:"profiles"` // "machine", "process", "filament" → profile name
}

// Profile is an uploaded slicer profile file.
type Profile struct {
	Name       string  `json:"name"` // file name in the profiles directory
	Kind       string  `json:"kind"` // machine, process or filament
	PricePerKg float64 `json:"price_per_kg,omitempty"`
}

type SlicerDefault struct {
	Supports    bool    `json:"supports"`
	SupportType string  `json:"support_type"`
	LayerHeight float64 `json:"layer_height"` // 0 = the process profile's
	Infill      int     `json:"infill"`       // percent; -1 = the process profile's
}

type Viewer struct {
	OverhangAngle float64 `json:"overhang_angle"`
	DisplayMode   string  `json:"display_mode"`
}

type Pipeline struct {
	ApprovalRequired bool     `json:"approval_required"` // always true; shown locked
	PreviewAngles    []string `json:"preview_angles"`
	Workspace        string   `json:"workspace"`
	LogLevel         string   `json:"log_level"`
}

var (
	PreviewAngles = []string{"iso", "front", "right", "top"}
	DisplayModes  = []string{"solid", "wireframe", "xray", "edges"}
	LogLevels     = []string{"debug", "info", "warn", "error"}
	SupportTypes  = []string{"tree(auto)", "normal(auto)", "tree(manual)", "normal(manual)"}
	ProfileKinds  = []string{slicer.ProfileMachine, slicer.ProfileProcess, slicer.ProfileFilament}
)

func Defaults() Settings {
	return Settings{
		Printers: []Printer{},
		Profiles: []Profile{},
		Slicer:   SlicerDefault{Supports: true, SupportType: "tree(auto)", Infill: -1},
		Viewer:   Viewer{OverhangAngle: 45, DisplayMode: "solid"},
		Pipeline: Pipeline{ApprovalRequired: true, PreviewAngles: []string{"iso", "front"}, Workspace: "/work", LogLevel: "info"},
		Currency: "EUR",
	}
}

// Default returns the default printer, or the first one.
func (s Settings) Default() (Printer, bool) {
	for _, p := range s.Printers {
		if p.Default {
			return p, true
		}
	}
	if len(s.Printers) > 0 {
		return s.Printers[0], true
	}
	return Printer{}, false
}

var ErrNoPrinter = errors.New("no printer configured: add one on the settings page (http://localhost:8080/settings)")

// Printer finds a printer by ID; an empty ID means the default printer.
func (s Settings) Printer(id string) (Printer, error) {
	if id == "" {
		p, ok := s.Default()
		if !ok {
			return Printer{}, ErrNoPrinter
		}
		return p, nil
	}
	for _, p := range s.Printers {
		if p.ID == id {
			return p, nil
		}
	}
	return Printer{}, fmt.Errorf("no printer with ID %q; list_printers shows the configured ones", id)
}

func (s Settings) Profile(name string) (Profile, bool) {
	for _, p := range s.Profiles {
		if p.Name == name {
			return p, true
		}
	}
	return Profile{}, false
}

var (
	idRe      = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	profileRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._()+-]{0,99}\.json$`)
)

// ValidProfileName reports whether name is safe to use as a file name in the profiles directory.
func ValidProfileName(name string) bool {
	return profileRe.MatchString(name) && !strings.Contains(name, "..")
}

// Validate checks every value; the error lists every problem at once.
func (s Settings) Validate() error {
	var errs []string
	bad := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }

	seenProfile := map[string]bool{}
	for _, p := range s.Profiles {
		if !ValidProfileName(p.Name) {
			bad("profile %q: name must end in .json and use letters, digits, spaces and . _ ( ) + -", p.Name)
		}
		if !contains(ProfileKinds, p.Kind) {
			bad("profile %q: kind must be machine, process or filament", p.Name)
		}
		if p.PricePerKg < 0 {
			bad("profile %q: price per kg cannot be negative", p.Name)
		}
		if seenProfile[p.Name] {
			bad("profile %q listed twice", p.Name)
		}
		seenProfile[p.Name] = true
	}

	ids, defaults := map[string]bool{}, 0
	for _, p := range s.Printers {
		where := fmt.Sprintf("printer %q", p.ID)
		if !idRe.MatchString(p.ID) {
			bad("%s: ID must be lowercase letters, digits, - or _ (max 32)", where)
		}
		if ids[p.ID] {
			bad("%s: ID used twice", where)
		}
		ids[p.ID] = true
		if strings.TrimSpace(p.Name) == "" {
			bad("%s: name is required", where)
		}
		if p.Default {
			defaults++
		}
		d, ok := printer.Lookup(p.Kind)
		if !ok {
			bad("%s: unknown printer kind %q", where, p.Kind)
			continue
		}
		for _, f := range d.Fields {
			v := p.Config[f.Key]
			switch {
			case f.Required && strings.TrimSpace(v) == "":
				bad("%s: %s is required", where, f.Label)
			case v == "":
			case f.Type == "ip" && net.ParseIP(v) == nil:
				bad("%s: %s %q is not an IP address", where, f.Label, v)
			case f.Type == "number":
				if _, err := strconv.ParseFloat(v, 64); err != nil {
					bad("%s: %s must be a number", where, f.Label)
				}
			case f.Type == "select" && !contains(f.Options, v):
				bad("%s: %s must be one of %s", where, f.Label, strings.Join(f.Options, ", "))
			}
		}
		for kind, name := range p.Profiles {
			if name == "" {
				continue
			}
			prof, ok := s.Profile(name)
			if !ok {
				bad("%s: %s profile %q is not uploaded", where, kind, name)
			} else if prof.Kind != kind {
				bad("%s: %q is a %s profile, not %s", where, name, prof.Kind, kind)
			}
		}
	}
	if defaults > 1 {
		bad("only one printer can be the default")
	}

	if s.Slicer.LayerHeight != 0 && (s.Slicer.LayerHeight < 0.04 || s.Slicer.LayerHeight > 0.6) {
		bad("layer height must be between 0.04 and 0.6 mm, or 0 to use the process profile")
	}
	if s.Slicer.Infill < -1 || s.Slicer.Infill > 100 {
		bad("infill must be 0 to 100 %%, or -1 to use the process profile")
	}
	if !contains(SupportTypes, s.Slicer.SupportType) {
		bad("support type must be one of %s", strings.Join(SupportTypes, ", "))
	}
	if s.Viewer.OverhangAngle < 1 || s.Viewer.OverhangAngle > 89 {
		bad("overhang angle must be between 1 and 89 degrees")
	}
	if !contains(DisplayModes, s.Viewer.DisplayMode) {
		bad("display mode must be one of %s", strings.Join(DisplayModes, ", "))
	}
	if !s.Pipeline.ApprovalRequired {
		bad("approval cannot be turned off: every print needs your explicit approval")
	}
	if len(s.Pipeline.PreviewAngles) == 0 {
		bad("pick at least one preview angle")
	}
	for _, a := range s.Pipeline.PreviewAngles {
		if !contains(PreviewAngles, a) {
			bad("preview angle %q must be one of %s", a, strings.Join(PreviewAngles, ", "))
		}
	}
	if !filepath.IsAbs(s.Pipeline.Workspace) {
		bad("workspace must be an absolute path")
	}
	if !contains(LogLevels, s.Pipeline.LogLevel) {
		bad("log level must be one of %s", strings.Join(LogLevels, ", "))
	}
	if !regexp.MustCompile(`^[A-Z]{3}$`).MatchString(s.Currency) {
		bad("currency must be a 3-letter code such as EUR or USD")
	}
	if len(errs) > 0 {
		return &ValidationError{Problems: errs}
	}
	return nil
}

type ValidationError struct{ Problems []string }

func (e *ValidationError) Error() string {
	return "invalid settings: " + strings.Join(e.Problems, "; ")
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Masked replaces every secret driver field with Mask, for the settings page.
func (s Settings) Masked() Settings {
	out := s.clone()
	for i, p := range out.Printers {
		for _, f := range secretFields(p.Kind) {
			if p.Config[f] != "" {
				out.Printers[i].Config[f] = Mask
			}
		}
	}
	return out
}

func secretFields(kind string) []string {
	d, ok := printer.Lookup(kind)
	if !ok {
		return nil
	}
	var out []string
	for _, f := range d.Fields {
		if f.Type == "secret" {
			out = append(out, f.Key)
		}
	}
	return out
}

func (s Settings) clone() Settings {
	b, _ := json.Marshal(s)
	var out Settings
	json.Unmarshal(b, &out)
	for i := range out.Printers {
		if out.Printers[i].Config == nil {
			out.Printers[i].Config = map[string]string{}
		}
		if out.Printers[i].Profiles == nil {
			out.Printers[i].Profiles = map[string]string{}
		}
	}
	return out
}

// Store reads and writes settings in the database.
type Store struct {
	db          *sql.DB
	profilesDir string
}

func NewStore(db *sql.DB, profilesDir string) *Store { return &Store{db: db, profilesDir: profilesDir} }

func (st *Store) ProfilesDir() string { return st.profilesDir }

// ProfilePath is where an uploaded profile lives.
func (st *Store) ProfilePath(name string) string {
	return filepath.Join(st.profilesDir, filepath.Base(name))
}

var sections = []string{"profiles", "slicer", "viewer", "pipeline", "currency"}

func (st *Store) Load() (Settings, error) {
	s := Defaults()
	rows, err := st.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return s, err
		}
		var target any
		switch k {
		case "profiles":
			target = &s.Profiles
		case "slicer":
			target = &s.Slicer
		case "viewer":
			target = &s.Viewer
		case "pipeline":
			target = &s.Pipeline
		case "currency":
			target = &s.Currency
		default:
			continue
		}
		if err := json.Unmarshal([]byte(v), target); err != nil {
			return s, fmt.Errorf("settings %s: %w", k, err)
		}
	}
	if err := rows.Err(); err != nil {
		return s, err
	}
	s.Pipeline.ApprovalRequired = true // locked on, whatever is stored

	prs, err := st.db.Query(`SELECT id, name, kind, is_default, config FROM printers ORDER BY created_at, id`)
	if err != nil {
		return s, err
	}
	defer prs.Close()
	for prs.Next() {
		var p Printer
		var cfg string
		if err := prs.Scan(&p.ID, &p.Name, &p.Kind, &p.Default, &cfg); err != nil {
			return s, err
		}
		var stored struct {
			Config   map[string]string `json:"config"`
			Profiles map[string]string `json:"profiles"`
		}
		if err := json.Unmarshal([]byte(cfg), &stored); err != nil {
			return s, fmt.Errorf("printer %s: %w", p.ID, err)
		}
		p.Config, p.Profiles = stored.Config, stored.Profiles
		if p.Config == nil {
			p.Config = map[string]string{}
		}
		if p.Profiles == nil {
			p.Profiles = map[string]string{}
		}
		s.Printers = append(s.Printers, p)
	}
	return s, prs.Err()
}

// Save validates and stores s. A secret field equal to Mask keeps the stored value.
func (st *Store) Save(s Settings) error {
	old, err := st.Load()
	if err != nil {
		return err
	}
	s = s.clone()
	for i, p := range s.Printers {
		for _, f := range secretFields(p.Kind) {
			if p.Config[f] != Mask {
				continue
			}
			s.Printers[i].Config[f] = ""
			for _, o := range old.Printers {
				if o.ID == p.ID && o.Kind == p.Kind {
					s.Printers[i].Config[f] = o.Config[f]
				}
			}
		}
	}
	if s.Profiles == nil {
		s.Profiles = []Profile{}
	}
	if s.Printers == nil {
		s.Printers = []Printer{}
	}
	if err := s.Validate(); err != nil {
		return err
	}
	for _, p := range s.Profiles {
		if _, err := os.Stat(st.ProfilePath(p.Name)); err != nil {
			return &ValidationError{Problems: []string{fmt.Sprintf("profile %q: file not found; upload it first", p.Name)}}
		}
	}

	tx, err := st.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	values := map[string]any{"profiles": s.Profiles, "slicer": s.Slicer, "viewer": s.Viewer, "pipeline": s.Pipeline, "currency": s.Currency}
	for _, k := range sections {
		b, _ := json.Marshal(values[k])
		if _, err := tx.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')`, k, string(b)); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM printers`); err != nil {
		return err
	}
	for _, p := range s.Printers {
		cfg, _ := json.Marshal(map[string]any{"config": p.Config, "profiles": p.Profiles})
		if _, err := tx.Exec(`INSERT INTO printers (id, name, kind, is_default, config) VALUES (?, ?, ?, ?, ?)`,
			p.ID, p.Name, p.Kind, p.Default, string(cfg)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SaveProfile stores an uploaded profile file and registers it (replacing one of the same name).
func (st *Store) SaveProfile(kind, name string, body []byte) (Profile, error) {
	if !contains(ProfileKinds, kind) {
		return Profile{}, &ValidationError{Problems: []string{"profile kind must be machine, process or filament"}}
	}
	if !ValidProfileName(name) {
		return Profile{}, &ValidationError{Problems: []string{fmt.Sprintf("profile file %q: name must end in .json and use letters, digits, spaces and . _ ( ) + -", name)}}
	}
	var probe map[string]any
	if err := json.Unmarshal(body, &probe); err != nil {
		return Profile{}, &ValidationError{Problems: []string{fmt.Sprintf("profile %q is not a JSON object: %v", name, err)}}
	}
	if err := os.MkdirAll(st.profilesDir, 0o755); err != nil {
		return Profile{}, err
	}
	if err := os.WriteFile(st.ProfilePath(name), body, 0o644); err != nil {
		return Profile{}, err
	}
	s, err := st.Load()
	if err != nil {
		return Profile{}, err
	}
	p := Profile{Name: name, Kind: kind}
	replaced := false
	for i, old := range s.Profiles {
		if old.Name == name {
			p.PricePerKg = old.PricePerKg
			s.Profiles[i] = p
			replaced = true
		}
	}
	if !replaced {
		s.Profiles = append(s.Profiles, p)
	}
	return p, st.Save(s)
}

// Bundle is the export file: settings with secrets, plus every profile file's contents.
type Bundle struct {
	Format   string            `json:"format"` // "printpipe-settings/1"
	Settings Settings          `json:"settings"`
	Files    map[string]string `json:"profile_files"` // name → JSON text
}

const BundleFormat = "printpipe-settings/1"

// Export returns the full setup. It includes access codes, so the page warns before download.
func (st *Store) Export() (Bundle, error) {
	s, err := st.Load()
	if err != nil {
		return Bundle{}, err
	}
	b := Bundle{Format: BundleFormat, Settings: s, Files: map[string]string{}}
	for _, p := range s.Profiles {
		body, err := os.ReadFile(st.ProfilePath(p.Name))
		if err != nil {
			return Bundle{}, fmt.Errorf("profile %q: %w", p.Name, err)
		}
		b.Files[p.Name] = string(body)
	}
	return b, nil
}

// Import replaces all settings and profiles with the bundle's, after validating it whole.
func (st *Store) Import(b Bundle) error {
	if b.Format != BundleFormat {
		return &ValidationError{Problems: []string{fmt.Sprintf("not a printpipe settings file (format %q)", b.Format)}}
	}
	if err := b.Settings.clone().Validate(); err != nil {
		return err
	}
	for _, p := range b.Settings.Profiles {
		body, ok := b.Files[p.Name]
		if !ok {
			return &ValidationError{Problems: []string{fmt.Sprintf("profile %q is listed but its file is missing", p.Name)}}
		}
		var probe map[string]any
		if json.Unmarshal([]byte(body), &probe) != nil {
			return &ValidationError{Problems: []string{fmt.Sprintf("profile %q is not JSON", p.Name)}}
		}
	}
	if err := os.MkdirAll(st.profilesDir, 0o755); err != nil {
		return err
	}
	for _, p := range b.Settings.Profiles {
		if err := os.WriteFile(st.ProfilePath(p.Name), []byte(b.Files[p.Name]), 0o644); err != nil {
			return err
		}
	}
	return st.Save(b.Settings)
}

// ProfilePaths resolves a printer's profiles to file paths, for the slicer.
func (st *Store) ProfilePaths(p Printer) map[string]string {
	out := map[string]string{}
	for kind, name := range p.Profiles {
		if name != "" {
			out[kind] = st.ProfilePath(name)
		}
	}
	return out
}
