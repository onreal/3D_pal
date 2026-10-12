package scad_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/margarit/printpipe/internal/scad"
)

func f(v float64) *float64 { return &v }

func TestParseParams(t *testing.T) {
	src := `// Overall size
size = 20; // [5:1:100]
wall = 2; // [1:0.5:5] Wall thickness in mm
height = 30; // [80]
chamfer = true;
label = "A1"; // Text on the lid
holes = 4; // [2, 4, 6]
fit = 0.2; // [0.1:Tight, 0.2:Normal, 0.3:Loose]
style = "round"; // [round, square]
offset = [1, 2, 3];
derived = size * 2; // expressions are not parameters

/* [Advanced] */
$fn = 64; // [16:128]

/* [Hidden] */
eps = 0.01;

module box() {
  inner = 5;
}
after = 1;
`
	got := scad.ParseParams(src)
	want := []scad.Param{
		{Name: "size", Type: "number", Value: 20.0, Min: f(5), Step: f(1), Max: f(100), Label: "Overall size"},
		{Name: "wall", Type: "number", Value: 2.0, Min: f(1), Step: f(0.5), Max: f(5), Label: "Wall thickness in mm"},
		{Name: "height", Type: "number", Value: 30.0, Min: f(0), Max: f(80)},
		{Name: "chamfer", Type: "boolean", Value: true},
		{Name: "label", Type: "string", Value: "A1", Label: "Text on the lid"},
		{Name: "holes", Type: "number", Value: 4.0, Options: []scad.Option{{2.0, "2"}, {4.0, "4"}, {6.0, "6"}}},
		{Name: "fit", Type: "number", Value: 0.2, Options: []scad.Option{{0.1, "Tight"}, {0.2, "Normal"}, {0.3, "Loose"}}},
		{Name: "style", Type: "string", Value: "round", Options: []scad.Option{{"round", "round"}, {"square", "square"}}},
		{Name: "offset", Type: "vector", Value: []float64{1, 2, 3}},
		{Name: "$fn", Type: "number", Value: 64.0, Min: f(16), Max: f(128), Group: "Advanced"},
	}
	if len(got) != len(want) {
		names := []string{}
		for _, p := range got {
			names = append(names, p.Name)
		}
		t.Fatalf("got %d params %v, want %d", len(got), names, len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("param %d:\n got %+v\nwant %+v", i, got[i], want[i])
		}
	}
}

func TestDefines(t *testing.T) {
	params := scad.ParseParams("size = 20;\nchamfer = true;\nlabel = \"A1\";\noffset = [1,2];\n")
	args, err := scad.Defines(params, map[string]any{"size": 25.5, "chamfer": false, "label": `say "hi"`, "offset": []any{3.0, 4.0}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-D", `chamfer=false`, "-D", `label="say \"hi\""`, "-D", "offset=[3,4]", "-D", "size=25.5"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("got %q", args)
	}
	if _, err := scad.Defines(params, map[string]any{"sise": 1.0}); err == nil || !strings.Contains(err.Error(), "size") {
		t.Fatalf("typo not caught: %v", err)
	}
	if _, err := scad.Defines(params, map[string]any{"size": "big"}); err == nil {
		t.Fatal("bad number accepted")
	}
}

func TestDefineTypesAndValues(t *testing.T) {
	num := scad.Param{Name: "n", Type: "number"}
	boolean := scad.Param{Name: "b", Type: "boolean"}
	vec := scad.Param{Name: "v", Type: "vector"}
	str := scad.Param{Name: "s", Type: "string"}
	ok := map[string]struct {
		p scad.Param
		v any
	}{
		"n=3":       {num, 3},
		"n=2.5":     {num, "2.5"},
		"n=7":       {num, int64(7)},
		"n=1.5":     {num, float32(1.5)},
		"b=true":    {boolean, "true"},
		"v=[1,2.5]": {vec, []float64{1, 2.5}},
		`s="x"`:     {str, "x"},
	}
	for want, c := range ok {
		got, err := scad.Define(c.p, c.v)
		if err != nil || got != want {
			t.Errorf("%v → %q %v, want %q", c.v, got, err, want)
		}
	}
	for _, c := range []struct {
		p scad.Param
		v any
	}{{num, true}, {boolean, 1}, {vec, "x"}, {vec, []any{"a"}}, {str, 1}, {scad.Param{Name: "q", Type: "weird"}, 1}} {
		if _, err := scad.Define(c.p, c.v); err == nil {
			t.Errorf("%s accepted %v", c.p.Type, c.v)
		}
	}
	vals := scad.Values([]scad.Param{{Name: "a", Value: 1.0}, {Name: "b", Value: 2.0}}, map[string]any{"b": 5.0})
	if vals["a"] != 1.0 || vals["b"] != 5.0 {
		t.Fatalf("%v", vals)
	}
}
