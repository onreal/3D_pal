# Modelling: the house format

Every model in the pipeline has the same layout, in this order. The templates
(`printpipe://templates/single-part`, `printpipe://templates/multi-part`) show all of it.

## 1. Header comment

```openscad
// =====================================================================
//  Snap Box: parametric box with a snap-on lid
//  OpenSCAD 2021.01+ (pipeline: nightly, Manifold). Units: mm.
// =====================================================================
//  PARTS (part = ...)            count  print orientation
//    base                        1      open side up
//    lid                         1      top face down
//  ASSEMBLY ORDER
//    1. Base.  2. Press the lid on until the bead clicks.
//  PRINT SETTINGS
//    PLA, 0.2 mm layers, 3 walls, 15 % infill, no supports.
//  TUNING
//    lid too tight -> lid_clearance +0.05      lid loose -> snap_bead +0.1
// =====================================================================
```

Keep this block true: update it with the code. It is the user's bill of materials and
manual.

## 2. Customizer parameters

Every value a user might change is a top-level assignment before the first module, in
Customizer form. The viewer builds sliders and dropdowns from them, and `compile_model`
takes overrides by name.

```openscad
/* [Part] */
part = "assembled"; // [assembled, exploded, base, lid, plate, coupon_fit, check_interference]

/* [Size] */
inner_w = 60;        // [20:1:200] Inside width in mm
wall = 2;            // [1.2:0.4:4] Wall thickness in mm, a multiple of 0.4

/* [Fit] */
lid_clearance = 0.2; // [0.1:0.05:0.5] Gap per side between lid and base in mm

/* [Features] */
dividers = 2;        // [0:1:6] Number of dividers
rounded = true;      // Round the vertical edges

/* [Cosmetic] */
label = "BOX";       // Text on the lid; empty for none
```

Rules:

- The comment holds `[min:step:max]`, or `[a, b, c]` for choices, then a description
  with its unit. Booleans need no range.
- Name things in `snake_case` with the kind of value last: `screw_d` (diameter), `boss_r`
  (radius), `wall` or `plate_t` (thickness), `post_h` (height), `n_slots` or `slots`
  (counts), `*_clearance` (gaps), `*_angle` (degrees).
- Every clearance is a parameter in the `[Fit]` group, defined **per side** (radial). Never
  write a bare `+0.2` inside a module.
- Only literal values go above `/* [Hidden] */`. Expressions go below it, or the
  Customizer can't edit them.
- Changing a value means `compile_model` with empty `scad_code` and `params`
  (`{"wall": 2.4}`). Don't rewrite the file to change a number.

## 3. Hidden section

```openscad
/* [Hidden] */
$fa = 2; $fs = 0.3;          // smooth curves without a fixed $fn
eps = 0.01;                  // overlap for booleans
outer_w = inner_w + 2 * wall;
// assembly declaration here (see assembly guide)
```

Prefer `$fa`/`$fs` to a global `$fn`: they scale facets with size. Use an explicit `$fn` only
where the count matters (`$fn = 6` for hex, matched mating circles).

## 4. Checks

Right after the derived values:

```openscad
assert(wall >= 1.2, "wall must be at least 1.2 mm (3 lines of a 0.4 nozzle)");
assert(outer_w <= 250, str("base is ", outer_w, " mm wide: too big for the bed"));
echo(str("INFO: outside ", outer_w, " x ", outer_d, " x ", outer_h, " mm"));
```

Use `assert` for anything that would make the part wrong (it stops the compile with your
message), and `echo("INFO: …")` for the few numbers a person wants to see. The compile
summary shows these lines. Keep it to fewer than 10 lines. See
[validation](validation.md).

## 5. Part modules

- One module per printed part, named after the part (`base()`, `lid()`), drawn **in its
  print orientation, resting on z = 0, near the origin in XY** (centred where the shape
  allows). A part's `part` option then just calls the module.
- `assembled()` moves parts with `translate()` and `rotate()` only, never `mirror()`.
- Modules that draw features (`screw_hole()`, `snap_bead()`) are separate and take their
  sizes as arguments.
- Mating features come from one shared definition: define the interface once (for
  example `lip_w`, `lip_h`), and build both sides from it with the clearance applied on
  one side.
- Booleans: cutters stick out `eps` past the faces they cut, and joined solids overlap by
  `eps`, so no faces are coincident.
- Prefer `hull()`, `offset()` and `rotate_extrude()` of 2D profiles to `minkowski()`,
  which is slow.
- Colours belong in `assembled()`, not in part modules.

## 6. Dispatcher

The last lines of the file:

```openscad
if (part == "assembled") assembled();
else if (part == "exploded") assembled(explode = 15);
else if (part == "base") base();
else if (part == "lid") lid();
else if (part == "plate") plate();
else if (part == "coupon_fit") coupon_fit();
else if (part == "check_interference") check_interference();
```

Option names:

| Option | What it shows |
| --- | --- |
| `assembled` | The finished object, every part in its real position and colour |
| `exploded` | The same, with parts pulled apart along the assembly axis |
| `section` | `assembled` cut in half, to see inside |
| `<part name>` | One printable part, flat on the bed |
| `plate`, `plate_<n>` | Several parts arranged to print together, within the bed |
| `coupon_<feature>` | A small test print of one fit (see [validation](validation.md)) |
| `check_<what>` | A diagnostic view, never printed (see [validation](validation.md)) |

Single-part models may skip `part` entirely. Add it as soon as there is a second printable
body, a coupon or a check.

## 7. Printability at a glance

Bambu Lab A1 class printer, 0.4 mm nozzle, 0.2 mm layers. The full rules are in
[printability](printability.md).

- Walls at least 1.2 mm (3 lines); 2 mm or more for strength.
- Overhangs up to 45° print without supports; the viewer marks steeper faces in pink.
- Clearance between moving parts 0.2–0.3 mm per side; press fits 0.05–0.1 mm.
- Holes print slightly small: use the hole sizes in [fits](fits.md).
- Build volume 256 × 256 × 256 mm on the A1 (check `list_printers`).
