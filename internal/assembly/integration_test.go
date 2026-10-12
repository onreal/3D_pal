//go:build integration

package assembly_test

import (
	"context"
	"os"
	"testing"

	"github.com/margarit/printpipe/internal/assembly"
	"github.com/margarit/printpipe/internal/runner"
	"github.com/margarit/printpipe/internal/scad"
)

const realStepped = `part = "assembled"; // [assembled, base, lid]
size = 30; // [20:60]
/* [Hidden] */
assembly = [["base", "Base"], ["pins", "Pins"], ["lid", "Lid"]];
assembly_only = "";
module step(id) { if (assembly_only == "" || assembly_only == id) children(); }
module assembled() {
  step("base") color("orange") cube([size, size, 3]);
  step("pins") color("dimgray") for (x = [5, size - 7]) translate([x, 5, 3.2]) cube([2, 2, 10]);
  step("lid") color("steelblue") translate([0, 0, 13.4]) cube([size, size, 3]);
}
if (part == "assembled") assembled();
else if (part == "base") cube([size, size, 3]);
else cube([size, size, 3]);
`

// Real OpenSCAD: one render per declared step, colours kept, pins counted as two.
func TestRealStepsAndPieces(t *testing.T) {
	c := scad.New(runner.Exec{}, os.Getenv("OPENSCAD"))
	c.UseBestBackend(context.Background())
	v := version(t, realStepped, map[string]any{"size": 40.0, "part": "base"})
	r := assembly.New(c, nil, nil).Build(context.Background(), v)
	if r.Status != "done" || r.Mode != assembly.Steps || len(r.Parts) != 3 {
		t.Fatalf("%+v", r)
	}
	for _, p := range r.Parts {
		if p.Error != "" {
			t.Fatalf("%s: %s", p.ID, p.Error)
		}
	}
	if pins := r.Parts[1]; pins.Count != 2 || pins.Min.Z < 3.1 || pins.Max.Z > 13.3 {
		t.Fatalf("pins: %+v", pins)
	}
	if lid := r.Parts[2]; lid.Max.X < 39.9 || lid.Color == r.Parts[0].Color {
		t.Fatalf("lid used the version's size and its own colour: %+v vs %+v", lid, r.Parts[0])
	}

	// Without a declaration: the assembled model split into pieces, bottom to top.
	plain := version(t, "size = 30;\ncolor(\"orange\") cube([size, size, 3]);\ncolor(\"steelblue\") translate([0, 0, 5]) cube([size, size, 3]);\n", nil)
	r = assembly.New(c, nil, nil).Build(context.Background(), plain)
	if r.Status != "done" || r.Mode != assembly.Pieces || len(r.Parts) != 2 || r.Parts[0].Max.Z > 3.1 {
		t.Fatalf("%+v", r)
	}
}
