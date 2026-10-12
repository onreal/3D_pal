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

// Flatten returns the preset at path with its whole "inherits" chain merged in.
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
		for k, v := range chain[i] {
			out[k] = v
		}
	}
	delete(out, "inherits")
	out["name"] = leaf["name"]
	return out, nil
}
