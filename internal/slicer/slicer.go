// Package slicer is the interface every slicer implements (PRD FR-24), and its registry.
package slicer

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/margarit/printpipe/internal/runner"
)

// Profile kinds every slicer entry is configured with.
const (
	ProfileMachine  = "machine"
	ProfileProcess  = "process"
	ProfileFilament = "filament"
)

// Overrides change the process profile for one job (PRD FR-9, FR-17). Zero values keep the profile's setting.
type Overrides struct {
	Supports    *bool   `json:"supports,omitempty"`
	SupportType string  `json:"support_type,omitempty"` // "tree(auto)", "normal(auto)"
	LayerHeight float64 `json:"layer_height,omitempty"` // mm
	Infill      *int    `json:"infill,omitempty"`       // percent
}

type Input struct {
	Model     string            // STL path
	OutPath   string            // sliced job path, e.g. /work/cube/v3/job.gcode.3mf
	Profiles  map[string]string // ProfileMachine, ProfileProcess, ProfileFilament → file path
	Overrides Overrides
}

type Filament struct {
	Type   string  `json:"type"` // "PLA"
	Grams  float64 `json:"grams"`
	Metres float64 `json:"metres"`
}

type Result struct {
	Path        string     `json:"path"` // sliced job file
	Seconds     int        `json:"seconds"`
	Filaments   []Filament `json:"filaments"`
	Layers      int        `json:"layers"`
	LayerHeight float64    `json:"layer_height"` // mm
	Supports    bool       `json:"supports"`     // the slicer generated supports
	OffPlate    bool       `json:"off_plate"`    // part lies outside the plate
}

type Slicer interface {
	Kind() string // matches printer.Capabilities.Slicer
	Slice(ctx context.Context, in Input) (Result, error)
}

type Deps struct {
	Runner runner.Runner
}

type Driver struct {
	Kind  string
	Label string
	New   func(Deps) Slicer
}

var (
	mu      sync.RWMutex
	drivers = map[string]Driver{}
)

func Register(d Driver) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := drivers[d.Kind]; dup {
		panic(fmt.Sprintf("slicer.Register: kind %q registered twice", d.Kind))
	}
	drivers[d.Kind] = d
}

// Set is the slicers available at run time, by kind.
type Set map[string]Slicer

// Registered builds every registered slicer with deps.
func Registered(deps Deps) Set {
	mu.RLock()
	defer mu.RUnlock()
	s := Set{}
	for k, d := range drivers {
		s[k] = d.New(deps)
	}
	return s
}

func (s Set) Get(kind string) (Slicer, error) {
	sl, ok := s[kind]
	if !ok {
		return nil, fmt.Errorf("no slicer of kind %q is installed", kind)
	}
	return sl, nil
}

func Kinds() []string {
	mu.RLock()
	defer mu.RUnlock()
	var out []string
	for k := range drivers {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
