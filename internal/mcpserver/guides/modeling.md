# Modelling for the print pipeline

How to write OpenSCAD models that work well with the viewer, the slicer and the user.

## Parameters the user can change

Put every dimension the user might change at the top level, in Customizer style, before
the first module. The viewer builds sliders and dropdowns from them:

```openscad
/* [Size] */
width = 40;       // [10:1:120] Width in mm
wall = 2;         // [1:0.5:5] Wall thickness in mm
/* [Fit] */
clearance = 0.2;  // [0.1:0.05:0.5] Gap between mating parts in mm
style = "round";  // [round, square]
/* [Hidden] */
$fn = 64;
```

To change a dimension, call `compile_model` with empty `scad_code` and `params`
(`{"wall": 3}`). Don't rewrite the file.

## Several parts in one file

Use a `part` parameter: one option per printable part, laid flat on the bed in its print
orientation, plus `"assembled"` for the whole object, and `"plate"` options that arrange
parts for printing together.

```openscad
part = "assembled"; // [assembled, base, lid, plate]
```

For an assembly timeline in the viewer, also declare the build order. See
`printpipe://guides/assembly`, or the `add_assembly_timeline` prompt.

## Printability (Bambu Lab A1, 0.4 mm nozzle)

- Walls are at least 1.2 mm (three perimeters); 2 mm or more for strength.
- Overhangs up to 45° print without supports; the viewer marks steeper faces in pink.
- Clearance between moving parts is 0.2–0.4 mm; press fits are 0.1–0.15 mm.
- Holes print slightly small: add 0.1–0.2 mm to the diameter, or give it a parameter.
- The build volume is 256 × 256 × 256 mm.

## Check your work

Look at the preview images `compile_model` returns. Then use `estimate_print` for time,
filament and cost, and `get_assembly` for the timeline.
