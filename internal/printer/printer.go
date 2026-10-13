// Package printer is everything the core knows about printers (PRD FR-22 to FR-25).
// Brand-specific protocols, formats and credentials live in driver packages.
package printer

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/margarit/printpipe/internal/runner"
)

type Volume struct{ X, Y, Z float64 } // millimetres

type Feature string

const (
	FeatureStatus Feature = "status"
	FeatureCancel Feature = "cancel"
	FeaturePause  Feature = "pause"
	FeatureCamera Feature = "camera"
)

type Capabilities struct {
	Model       string    `json:"model"`        // "Bambu Lab A1"
	BuildVolume Volume    `json:"build_volume"` // the viewer draws this; the pipeline checks fit against it
	BedShape    string    `json:"bed_shape"`    // "rect" or "round"
	JobFormat   string    `json:"job_format"`   // ".gcode.3mf", ".gcode", ".bgcode"
	Slicer      string    `json:"slicer"`       // slicer kind this printer needs, e.g. "bambustudio"
	Features    []Feature `json:"features"`
}

func (c Capabilities) Has(f Feature) bool {
	for _, x := range c.Features {
		if x == f {
			return true
		}
	}
	return false
}

type State string

const (
	Idle     State = "idle"
	Printing State = "printing"
	Paused   State = "paused"
	Finished State = "finished"
	Failed   State = "failed"
	Offline  State = "offline"
	// Unknown: the printer is on the network but its status channel doesn't answer; it may
	// be printing.
	Unknown State = "unknown"
)

type Status struct {
	State       State   `json:"state"`
	Progress    float64 `json:"progress"` // 0 to 1
	Layer       int     `json:"layer"`
	TotalLayers int     `json:"total_layers"`
	Message     string  `json:"message,omitempty"`
	// Remaining is the printer's own estimate of the time left, in seconds (0 = unknown).
	Remaining int `json:"remaining_s,omitempty"`
	// JobName is the job the printer runs, as uploaded without the extension ("cube_v3"),
	// when the printer reports it. It finds the job again after a server restart.
	JobName string `json:"job_name,omitempty"`
}

type Job struct{ Path, Name string } // local file in the printer's JobFormat

type Remote struct{ ID string } // driver-defined handle, e.g. the path on the SD card

// Printer is everything the core knows about a printer.
type Printer interface {
	Capabilities() Capabilities
	Test(ctx context.Context) error
	Upload(ctx context.Context, job Job) (Remote, error)
	Start(ctx context.Context, r Remote) error
	Status(ctx context.Context) (Status, error) // ErrUnsupported without FeatureStatus
	Cancel(ctx context.Context) error           // ErrUnsupported without FeatureCancel
}

// Check is one line of a connection test, for the settings page.
type Check struct {
	Name    string `json:"name"` // "FTPS 990", "MQTT 8883", "Login"
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// Diagnoser is optional: drivers that can report each step of a connection test implement it.
type Diagnoser interface {
	Diagnose(ctx context.Context) []Check
}

// Shared errors: every driver maps its protocol errors onto these.
var (
	ErrUnreachable = errors.New("printer unreachable")
	ErrAuth        = errors.New("printer rejected the credentials")
	ErrBusy        = errors.New("printer is busy")
	ErrNoStorage   = errors.New("printer storage missing or full")
	ErrUnsupported = errors.New("not supported by this printer")
	ErrNotUploaded = errors.New("that job was not uploaded to this printer")
	ErrRefused     = errors.New("printer refused the command")
)

// Field describes one config value, so the settings page can render the driver's form.
type Field struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Type     string   `json:"type"` // "text", "ip", "secret", "number", "select"
	Help     string   `json:"help,omitempty"`
	Required bool     `json:"required"`
	Default  string   `json:"default,omitempty"`
	Options  []string `json:"options,omitempty"` // for "select"
}

// Deps are the seams a driver may use; main wires real ones, tests wire fakes.
type Deps struct {
	Runner runner.Runner
}

type Driver struct {
	Kind   string                                                  `json:"kind"`  // "bambu-lan", "fake"
	Label  string                                                  `json:"label"` // "Bambu Lab (LAN, Developer Mode)"
	Fields []Field                                                 `json:"fields"`
	New    func(cfg map[string]string, deps Deps) (Printer, error) `json:"-"`
}

var (
	mu      sync.RWMutex
	drivers = map[string]Driver{}
)

// Register is called by drivers from init().
func Register(d Driver) {
	mu.Lock()
	defer mu.Unlock()
	if d.Kind == "" || d.New == nil {
		panic("printer.Register: driver needs a kind and a constructor")
	}
	if _, dup := drivers[d.Kind]; dup {
		panic(fmt.Sprintf("printer.Register: kind %q registered twice", d.Kind))
	}
	drivers[d.Kind] = d
}

func Lookup(kind string) (Driver, bool) {
	mu.RLock()
	defer mu.RUnlock()
	d, ok := drivers[kind]
	return d, ok
}

// Drivers lists registered drivers sorted by kind.
func Drivers() []Driver {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Driver, 0, len(drivers))
	for _, d := range drivers {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}

// Fits reports whether a part of the given size fits the build volume, allowing a
// 90° turn on the bed (the slicer's auto-orient and arrange may rotate it).
func Fits(c Capabilities, size Volume) bool {
	v := c.BuildVolume
	if size.Z > v.Z {
		return false
	}
	if c.BedShape == "round" { // diameter is X; the footprint's diagonal must fit
		return size.X*size.X+size.Y*size.Y <= v.X*v.X
	}
	return (size.X <= v.X && size.Y <= v.Y) || (size.Y <= v.X && size.X <= v.Y)
}
