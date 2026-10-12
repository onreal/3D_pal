# Troubleshooting

## Compile errors

| Message | Cause | Fix |
| --- | --- | --- |
| `Parser error … line N` | Syntax: a missing `;`, brace or comma | Look at line N and the line before it |
| `Assertion '…' failed: "<message>"` | One of the model's own checks | Do what the message says, or fix the parameter |
| `the top-level object is empty` | Nothing drawn: the dispatcher matched no option, a `difference()` removed everything, or a `check_*` view without a marker | Check the `part` value against the options; add the marker cube to checks |
| `WARNING: Ignoring unknown variable` | Typo, or a variable defined in another scope | Fix the name; module-local variables are invisible outside |
| `WARNING: Ignoring unknown module` | Typo, or a module that isn't defined | Fix the name. There are no libraries (BOSL2, MCAD) |
| `WARNING: … font …` | The font isn't installed | `"Liberation Sans:style=Bold"`, or `"DejaVu Sans:style=Bold"` in the pipeline image |
| `WARNING: Too many unnamed arguments` | Wrong argument order | Name the arguments: `cylinder(h = 5, d = 3)` |
| Variable has the wrong value | OpenSCAD variables are set once per scope; the last assignment wins | Don't reassign; compute a new name |
| `assembly steps: …` | The assembly declaration is incomplete | See [assembly](assembly.md): `assembly`, `assembly_only` and `step()` all present |

## Slow compiles

The pipeline uses the Manifold engine, which is fast. Most slow models have one of these:

- `minkowski()` with a sphere: use `hull()` of spheres, or `offset()` on a 2D profile, then
  extrude.
- A global `$fn` of 100 or more: use `$fa = 2; $fs = 0.3;` and raise `$fn` only where
  needed.
- Thousands of small objects (knurls, perforations): build the pattern in 2D and extrude
  it, or `linear_extrude` a star with `twist` for knurls.
- Text in a loop: render it once inside a module.
- A big `check_interference`: leave out cosmetic detail (texture, text) from the parts in
  the check.

## Previews look wrong

| What you see | Fix |
| --- | --- |
| The part floats, or goes below the bed | The part module doesn't rest on z = 0: translate it so its lowest point is at 0 |
| `assembled` shows parts in the wrong place | Part modules are in print orientation; `assembled()` must flip and lift them |
| A feature is missing | A cutter didn't reach (`eps`), or a `difference()` removed it: check the order of children |
| Thin flickering skins | Coincident faces: extend cutters by `eps` past the faces |
| Text reads backwards in `assembled` | The placement uses `mirror()`: turn the part over with `rotate()` instead |
| Text reads backwards on the print | It's on the bed face: `mirror([1, 0, 0])` the text itself (only the text), or move it to the top |
| Previews don't show the inside | Compile `section` |

## Print problems → parameters

| Problem after printing | Change |
| --- | --- |
| Parts don't go together | The fit's clearance +0.05 per side; check the coupon first |
| Parts rattle | That clearance −0.05 |
| Gears bind | `backlash` +0.1, `elephant_foot` +0.05; look for a stuck first layer |
| Snap won't close | Snap engagement −0.1, or a longer, thinner arm |
| Snap pops open | Engagement +0.1, a steeper retaining face, or a stiffer arm |
| Snap arm breaks | Strain too high: longer arm or less engagement; arms must lie flat |
| Holes too small | That hole's clearance +0.1 |
| Bottom edges flared | `elephant_foot` +0.05, or a bigger bed-edge chamfer |
| Corners lift | Round the corners, add a brim, clean the bed |
| Sagging roofs or holes | Teardrop the hole, shorten the bridge, or flip the part |
| Text unreadable | Bigger text (≥ 4 mm cap height), deeper (0.6 mm), bold font |

Every number in the right-hand column is a model parameter. Change it with
`compile_model` params, compile the coupon again, and print the coupon before the part.
