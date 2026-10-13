// Package fake is an in-memory printer for development, demos and end-to-end tests (PRD FR-28).
package fake

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/margarit/printpipe/internal/printer"
)

func init() {
	printer.Register(printer.Driver{
		Kind:  "fake",
		Label: "Fake printer (simulated, for testing)",
		Fields: []printer.Field{
			{Key: "print_seconds", Label: "Simulated print time (seconds)", Type: "number", Default: "60"},
			{Key: "slicer", Label: "Slicer", Type: "select", Default: "bambustudio",
				Options: []string{"bambustudio", "fake"}, Help: "fake skips real slicing; use it without Bambu Studio"},
		},
		New: func(cfg map[string]string, _ printer.Deps) (printer.Printer, error) {
			opts := []Option{}
			if s := cfg["print_seconds"]; s != "" {
				n, err := strconv.Atoi(s)
				if err != nil || n <= 0 {
					return nil, fmt.Errorf("print_seconds must be a positive number, got %q", s)
				}
				opts = append(opts, PrintTime(time.Duration(n)*time.Second))
			}
			if s := cfg["slicer"]; s != "" {
				opts = append(opts, Slicer(s))
			}
			return New(opts...), nil
		},
	})
}

type Option func(*Printer)

// FailWith makes every transport call fail with err (a shared printer error).
func FailWith(err error) Option { return func(p *Printer) { p.fail = err } }

func PrintTime(d time.Duration) Option { return func(p *Printer) { p.printTime = d } }

func Clock(now func() time.Time) Option { return func(p *Printer) { p.now = now } }

func Slicer(kind string) Option { return func(p *Printer) { p.slicer = kind } }

type Printer struct {
	mu        sync.Mutex
	fail      error
	printTime time.Duration
	now       func() time.Time
	slicer    string
	uploaded  map[string]bool
	uploads   int
	starts    int
	started   time.Time
	printing  bool
	refuse    error
}

// RefuseStart makes Start fail with err after a successful upload, like a printer that turns
// the start command down; nil accepts again.
func (p *Printer) RefuseStart(err error) { p.mu.Lock(); p.refuse = err; p.mu.Unlock() }

func New(opts ...Option) *Printer {
	p := &Printer{printTime: time.Minute, now: time.Now, slicer: "bambustudio", uploaded: map[string]bool{}}
	for _, o := range opts {
		o(p)
	}
	return p
}

func (p *Printer) Capabilities() printer.Capabilities {
	return printer.Capabilities{
		Model:       "Fake printer",
		BuildVolume: printer.Volume{X: 256, Y: 256, Z: 256},
		BedShape:    "rect",
		JobFormat:   ".gcode.3mf",
		Slicer:      p.slicer,
		Features:    []printer.Feature{printer.FeatureStatus, printer.FeatureCancel},
	}
}

func (p *Printer) Test(context.Context) error { return p.fail }

func (p *Printer) Diagnose(ctx context.Context) []printer.Check {
	if err := p.Test(ctx); err != nil {
		return []printer.Check{{Name: "Simulated printer", Message: err.Error()}}
	}
	return []printer.Check{{Name: "Simulated printer", OK: true, Message: "ready"}}
}

func (p *Printer) Upload(_ context.Context, job printer.Job) (printer.Remote, error) {
	if p.fail != nil {
		return printer.Remote{}, p.fail
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.uploads++
	p.uploaded[job.Name] = true
	return printer.Remote{ID: job.Name}, nil
}

func (p *Printer) Start(_ context.Context, r printer.Remote) error {
	if p.fail != nil {
		return p.fail
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.uploaded[r.ID] {
		return fmt.Errorf("%s: %w", r.ID, printer.ErrNotUploaded)
	}
	if p.refuse != nil {
		return p.refuse
	}
	if p.printing && p.now().Sub(p.started) < p.printTime {
		return printer.ErrBusy
	}
	p.starts++
	p.started, p.printing = p.now(), true
	return nil
}

func (p *Printer) Status(context.Context) (printer.Status, error) {
	if p.fail != nil {
		return printer.Status{State: printer.Offline}, p.fail
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.printing {
		return printer.Status{State: printer.Idle}, nil
	}
	const layers = 100
	frac := float64(p.now().Sub(p.started)) / float64(p.printTime)
	if frac >= 1 {
		return printer.Status{State: printer.Finished, Progress: 1, Layer: layers, TotalLayers: layers}, nil
	}
	return printer.Status{State: printer.Printing, Progress: frac, Layer: int(frac * layers), TotalLayers: layers}, nil
}

func (p *Printer) Cancel(context.Context) error {
	if p.fail != nil {
		return p.fail
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.printing = false
	return nil
}

func (p *Printer) Uploads() int { p.mu.Lock(); defer p.mu.Unlock(); return p.uploads }
func (p *Printer) Starts() int  { p.mu.Lock(); defer p.mu.Unlock(); return p.starts }
