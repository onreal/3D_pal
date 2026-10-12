package fake_test

import (
	"context"
	"testing"
	"time"

	"github.com/margarit/printpipe/internal/printer"
	"github.com/margarit/printpipe/internal/printer/printertest"
	"github.com/margarit/printpipe/internal/printers/fake"
)

func TestFakeDriverContract(t *testing.T) {
	printertest.Run(t, printertest.Harness{
		New:    func(t *testing.T) printer.Printer { return fake.New() },
		Broken: func(t *testing.T, want error) printer.Printer { return fake.New(fake.FailWith(want)) },
	})
}

func TestFakeCountsAndFinishes(t *testing.T) {
	now := time.Unix(0, 0)
	p := fake.New(fake.PrintTime(10*time.Second), fake.Clock(func() time.Time { return now }))
	ctx := context.Background()
	r, _ := p.Upload(ctx, printer.Job{Path: "x", Name: "x.gcode.3mf"})
	p.Start(ctx, r)
	if p.Uploads() != 1 || p.Starts() != 1 {
		t.Fatalf("uploads=%d starts=%d", p.Uploads(), p.Starts())
	}
	now = now.Add(5 * time.Second)
	if st, _ := p.Status(ctx); st.State != printer.Printing || st.Progress != 0.5 {
		t.Fatalf("halfway: %+v", st)
	}
	now = now.Add(6 * time.Second)
	if st, _ := p.Status(ctx); st.State != printer.Finished || st.Progress != 1 {
		t.Fatalf("done: %+v", st)
	}
}

func TestFakeBusyWhilePrinting(t *testing.T) {
	p := fake.New()
	ctx := context.Background()
	r, _ := p.Upload(ctx, printer.Job{Name: "a.gcode.3mf"})
	p.Start(ctx, r)
	if err := p.Start(ctx, r); err == nil {
		t.Fatal("second start while printing must fail")
	}
	p.Cancel(ctx)
	if st, _ := p.Status(ctx); st.State != printer.Idle {
		t.Fatalf("after cancel: %+v", st)
	}
}

func TestDriverRegistered(t *testing.T) {
	d, ok := printer.Lookup("fake")
	if !ok {
		t.Fatal("fake not registered")
	}
	p, err := d.New(map[string]string{"print_seconds": "3"}, printer.Deps{})
	if err != nil || p.Capabilities().Model == "" {
		t.Fatalf("%v %+v", err, p)
	}
}
