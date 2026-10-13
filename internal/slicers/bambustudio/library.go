package bambustudio

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// Library is Bambu Studio's system presets (resources/profiles/BBL). Presets inherit
// from parents by name, and the command line does not resolve that chain: a preset
// passed as-is slices with zero filament density and no printer model. Flatten merges
// the whole chain into one self-contained preset first. This also makes user presets
// exported from Bambu Studio work, since they hold only their differences and an
// "inherits" link to a system preset.
type Library struct {
	root  string
	once  sync.Once
	index map[string]map[string]string // kind → preset name → file
	err   error

	catMu sync.Mutex
	cats  map[string]*catalogue // printer model → its presets
}

func NewLibrary(root string) *Library { return &Library{root: root} }

// Available reports whether the system presets are installed.
func (l *Library) Available() bool {
	st, err := os.Stat(l.root)
	return l.root != "" && err == nil && st.IsDir()
}

func (l *Library) load() {
	l.index = map[string]map[string]string{}
	l.err = filepath.WalkDir(l.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".json" {
			return err
		}
		rel, _ := filepath.Rel(l.root, path)
		kind := firstDir(rel)
		var head struct {
			Name string `json:"name"`
		}
		b, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(b, &head) != nil || head.Name == "" {
			return nil
		}
		if l.index[kind] == nil {
			l.index[kind] = map[string]string{}
		}
		l.index[kind][head.Name] = path
		return nil
	})
}

func firstDir(rel string) string {
	for {
		dir := filepath.Dir(rel)
		if dir == "." || dir == string(filepath.Separator) {
			return rel
		}
		rel = dir
	}
}

func (l *Library) find(kind, name string) (string, bool) {
	l.once.Do(l.load)
	if p, ok := l.index[kind][name]; ok {
		return p, true
	}
	for _, names := range l.index { // some parents live under another kind's folder
		if p, ok := names[name]; ok {
			return p, true
		}
	}
	return "", false
}

func readPreset(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s is not a JSON preset: %w", filepath.Base(path), err)
	}
	return m, nil
}

// DefaultBedType is the build plate a printer model ships with ("Textured PEI Plate" for
// the A1), from its machine_model file; "" when unknown.
func (l *Library) DefaultBedType(model string) string {
	if model == "" || !l.Available() {
		return ""
	}
	p, ok := l.find("machine", model)
	if !ok {
		return ""
	}
	m, err := readPreset(p)
	if err != nil {
		return ""
	}
	s, _ := m["default_bed_type"].(string)
	return s
}

// Flatten returns the preset at path with its whole "inherits" chain merged in, and the
// templates each preset includes.
func (l *Library) Flatten(kind, path string) (map[string]any, error) {
	leaf, err := readPreset(path)
	if err != nil {
		return nil, err
	}
	chain := []map[string]any{leaf}
	seen := map[string]bool{}
	cur := leaf
	for {
		parent, _ := cur["inherits"].(string)
		if parent == "" {
			break
		}
		if seen[parent] || len(chain) > 32 {
			return nil, fmt.Errorf("%s preset %s: inheritance loop at %q", kind, filepath.Base(path), parent)
		}
		seen[parent] = true
		if !l.Available() {
			return nil, fmt.Errorf("%s preset %s inherits %q, but Bambu Studio's system presets are not installed", kind, filepath.Base(path), parent)
		}
		p, ok := l.find(kind, parent)
		if !ok {
			return nil, fmt.Errorf("%s preset %s inherits %q, which is not one of this Bambu Studio's system presets: export the full preset, or use a matching Bambu Studio version", kind, filepath.Base(path), parent)
		}
		cur, err = readPreset(p)
		if err != nil {
			return nil, err
		}
		chain = append(chain, cur)
	}
	out := map[string]any{}
	for i := len(chain) - 1; i >= 0; i-- { // root first, the leaf last wins
		// Bambu Studio 2 keeps long values such as the A1's start G-code in template presets
		// that a preset pulls in with "include"; the preset's own keys win over them.
		for _, name := range stringList(chain[i]["include"]) {
			if !l.Available() {
				return nil, fmt.Errorf("%s preset %s includes %q, but Bambu Studio's system presets are not installed", kind, filepath.Base(path), name)
			}
			p, ok := l.find(kind, name)
			if !ok {
				return nil, fmt.Errorf("%s preset %s includes %q, which is not one of this Bambu Studio's presets", kind, filepath.Base(path), name)
			}
			inc, err := readPreset(p)
			if err != nil {
				return nil, err
			}
			for k, v := range inc {
				if k != "name" && k != "instantiation" && k != "from" && k != "type" {
					out[k] = v
				}
			}
		}
		for k, v := range chain[i] {
			out[k] = v
		}
	}
	delete(out, "inherits")
	delete(out, "include")
	// Bambu Studio checks compatibility by preset name (a process lists the machines it
	// suits), so the flattened preset takes the name of the nearest system preset it
	// derives from; a user preset's own name would read as an unknown printer.
	out["name"] = leaf["name"]
	for _, p := range chain {
		if p["from"] == "system" && p["instantiation"] == "true" {
			// Marked as a system preset too: the CLI applies stricter compatibility rules to
			// "User" presets and rejects a flattened one ("process not compatible with printer").
			out["name"], out["from"] = p["name"], "system"
			break
		}
	}
	// The settings IDs are what the compatibility check compares; keep them in step.
	for _, id := range []string{"printer_settings_id", "print_settings_id"} {
		if _, ok := out[id]; ok {
			out[id] = out["name"]
		}
	}
	if _, ok := out["filament_settings_id"]; ok {
		out["filament_settings_id"] = []any{out["name"]}
	}
	return out, nil
}
