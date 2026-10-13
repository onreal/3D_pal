package bambustudio

import (
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/margarit/printpipe/internal/slicer"
)

// presetHead is the part of a system preset file the catalogue needs.
type presetHead struct {
	Type               string   `json:"type"`
	Name               string   `json:"name"`
	Instantiation      string   `json:"instantiation"`
	Inherits           string   `json:"inherits"`
	PrinterModel       string   `json:"printer_model"`
	PrinterVariant     string   `json:"printer_variant"`
	CompatiblePrinters []string `json:"compatible_printers"`
	DefaultProcess     string   `json:"default_print_profile"`
	DefaultFilaments   []string `json:"default_filament_profile"`
}

type catalogue struct {
	once    sync.Once
	presets []slicer.Preset
}

// Presets lists the system presets Bambu Studio offers for a printer model ("Bambu Lab A1"):
// its machines (one per nozzle), and the processes and filaments made for them. The 0.4 mm
// nozzle machine and the process and filament it names as its defaults are marked Default;
// they are what a printer without uploaded profiles slices with.
func (l *Library) Presets(model string) []slicer.Preset {
	if model == "" || !l.Available() {
		return nil
	}
	l.catMu.Lock()
	if l.cats == nil {
		l.cats = map[string]*catalogue{}
	}
	c := l.cats[model]
	if c == nil {
		c = &catalogue{}
		l.cats[model] = c
	}
	l.catMu.Unlock()
	c.once.Do(func() { c.presets = l.catalogue(model) })
	return c.presets
}

func (l *Library) catalogue(model string) []slicer.Preset {
	l.once.Do(l.load)
	heads := map[string]map[string]presetHead{} // kind → name → head
	for _, kind := range []string{slicer.ProfileMachine, slicer.ProfileProcess, slicer.ProfileFilament} {
		heads[kind] = map[string]presetHead{}
		for name, path := range l.index[kind] {
			var h presetHead
			if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &h) == nil && h.Instantiation == "true" {
				heads[kind][name] = h
			}
		}
	}
	machines := map[string]presetHead{}
	var def presetHead
	for name, h := range heads[slicer.ProfileMachine] {
		if h.PrinterModel != model {
			continue
		}
		machines[name] = h
		if def.Name == "" || h.PrinterVariant == "0.4" {
			def = h
		}
	}
	if len(machines) == 0 {
		return nil
	}
	defFilament := ""
	if len(def.DefaultFilaments) > 0 {
		defFilament = def.DefaultFilaments[0]
	}
	out := []slicer.Preset{}
	add := func(kind, name string, isDefault bool) {
		p := slicer.Preset{Kind: kind, Name: name, Path: l.index[kind][name], Default: isDefault}
		if kind == slicer.ProfileFilament {
			if m, err := l.Flatten(kind, p.Path); err == nil {
				p.PricePerKg = firstFloat(m["filament_cost"])
			}
		}
		out = append(out, p)
	}
	for name := range machines {
		add(slicer.ProfileMachine, name, name == def.Name)
	}
	for _, kind := range []string{slicer.ProfileProcess, slicer.ProfileFilament} {
		for name, h := range heads[kind] {
			compat := h.CompatiblePrinters
			if compat == nil { // inherited from a parent
				if m, err := l.Flatten(kind, l.index[kind][name]); err == nil {
					compat = stringList(m["compatible_printers"])
				}
			}
			for _, c := range compat {
				if _, ok := machines[c]; ok {
					add(kind, name, name == def.DefaultProcess || name == defFilament)
					break
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return kindOrder(out[i].Kind) < kindOrder(out[j].Kind)
		}
		if out[i].Default != out[j].Default {
			return out[i].Default
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func kindOrder(k string) int {
	return map[string]int{slicer.ProfileMachine: 0, slicer.ProfileProcess: 1, slicer.ProfileFilament: 2}[k]
}

func stringList(v any) []string {
	l, _ := v.([]any)
	out := make([]string, 0, len(l))
	for _, x := range l {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func firstFloat(v any) float64 {
	s := ""
	switch x := v.(type) {
	case []any:
		if len(x) > 0 {
			s, _ = x[0].(string)
		}
	case string:
		s = x
	}
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

// Presets lists the system presets for a printer model (see Library.Presets).
func (s *Slicer) Presets(model string) []slicer.Preset { return s.lib.Presets(model) }
