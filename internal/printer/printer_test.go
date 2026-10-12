package printer_test

import (
	"context"
	"testing"

	"github.com/margarit/printpipe/internal/printer"
)

func TestFits(t *testing.T) {
	a1 := printer.Capabilities{BuildVolume: printer.Volume{X: 256, Y: 256, Z: 256}, BedShape: "rect"}
	narrow := printer.Capabilities{BuildVolume: printer.Volume{X: 300, Y: 100, Z: 100}, BedShape: "rect"}
	round := printer.Capabilities{BuildVolume: printer.Volume{X: 200, Y: 200, Z: 100}, BedShape: "round"}
	cases := []struct {
		name string
		c    printer.Capabilities
		size printer.Volume
		want bool
	}{
		{"cube fits", a1, printer.Volume{X: 20, Y: 20, Z: 20}, true},
		{"exact fit", a1, printer.Volume{X: 256, Y: 256, Z: 256}, true},
		{"too tall", a1, printer.Volume{X: 20, Y: 20, Z: 257}, false},
		{"too wide", a1, printer.Volume{X: 300, Y: 20, Z: 20}, false},
		{"fits turned", narrow, printer.Volume{X: 50, Y: 250, Z: 20}, true},
		{"round fits", round, printer.Volume{X: 140, Y: 140, Z: 20}, true},
		{"round corner sticks out", round, printer.Volume{X: 150, Y: 150, Z: 20}, false},
	}
	for _, c := range cases {
		if got := printer.Fits(c.c, c.size); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}

func TestRegistry(t *testing.T) {
	printer.Register(printer.Driver{Kind: "test-kind", New: func(map[string]string, printer.Deps) (printer.Printer, error) { return nil, nil }})
	if _, ok := printer.Lookup("test-kind"); !ok {
		t.Fatal("registered driver not found")
	}
	if _, ok := printer.Lookup("nope"); ok {
		t.Fatal("unknown kind found")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate kind must panic")
		}
	}()
	printer.Register(printer.Driver{Kind: "test-kind", New: func(map[string]string, printer.Deps) (printer.Printer, error) { return nil, nil }})
}

func TestHas(t *testing.T) {
	c := printer.Capabilities{Features: []printer.Feature{printer.FeatureStatus}}
	if !c.Has(printer.FeatureStatus) || c.Has(printer.FeatureCancel) {
		t.Fatal("Has is wrong")
	}
	_ = context.Background()
}
