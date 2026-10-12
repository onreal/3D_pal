# Fits, hardware and mechanisms

Starting values for PLA on a tuned 0.4 mm nozzle printer. Every one of them is a `[Fit]`
parameter in the model, so the user can tune from a test coupon without editing code.
**All clearances are per side (radial)**: a 5 mm pin in a hole with 0.2 mm clearance
needs a 5.4 mm hole.

## Clearances

| Fit | Per side | Use |
| --- | --- | --- |
| Press (interference) | −0.05 to −0.1 | Pegs that must not come out; chamfer the entry |
| Snug, locating | 0.1–0.15 | Keys, dowels, hex shafts, lids that should not rattle |
| Sliding | 0.2 | Drawers, sliders, parts you take apart often |
| Running (rotates) | 0.2–0.3 | Gears on axles, wheels, caps |
| Loose | 0.4 | Parts that pass each other but never touch |
| Print-in-place | 0.3–0.4 in XY, ≥ 0.3 in Z | Hinges and joints printed already assembled |

- PETG: add 0.05. TPU: add 0.1. Printers that aren't tuned: start 0.05 looser.
- **Axial gaps** between stacked moving parts: at least one layer (0.2 mm).
- Vertical holes print smaller than horizontal ones; holes on the bed face shrink most:
  chamfer their mouth.

## Hardware

Hole sizes as printed in PLA, for metric screws:

| Size | Clearance hole | Thread into plastic (self-tap) | Hex nut trap (across flats × depth) | Socket head counterbore (d × depth) | Heat-set insert hole |
| --- | --- | --- | --- | --- | --- |
| M2 | 2.4 | 1.7 | 4.2 × 1.8 | 4.2 × 2.2 | 3.2 |
| M2.5 | 2.9 | 2.1 | 5.2 × 2.2 | 5.0 × 2.7 | 3.6 |
| M3 | 3.4 | 2.6 | 5.7 × 2.6 | 6.0 × 3.2 | 4.0 (5.7 mm long insert: 6.5 deep) |
| M4 | 4.5 | 3.4 | 7.2 × 3.4 | 7.6 × 4.2 | 5.6 |
| M5 | 5.5 | 4.3 | 8.2 × 4.2 | 9.2 × 5.2 | 6.4 |

- Inserts: check the insert's datasheet; leave at least 1.6 mm of wall around the hole.
- 90° countersink for M3: head 6.4 mm. Print countersinks facing up, or the cone overhangs.
- Nut traps you print upright are hexagons with a point upwards (`rotate(30)` for a
  `$fn = 6` cylinder), so the flat roof bridges.
- 608 bearing (22 × 8 × 7): housing 22.1–22.2 mm for a press fit, shaft 8.0–8.1.
- Round magnets: pocket = magnet d + 0.15 (press) or + 0.3 (glued), depth + 0.2.

## Snap fits

A cantilever arm of length `L`, thickness `t` (in the bending direction) and deflection
`δ` reaches a strain of about `ε = 1.5 · t · δ / L²`.

| Material | Strain, assembled once | Strain, repeated |
| --- | --- | --- |
| PLA | ≤ 2 % | ≤ 1 % |
| PETG | ≤ 3.5 % | ≤ 2 % |

- Put the formula in the model and `echo` the strain; `assert` it stays under the limit.
- Arms **lie flat** (bend in XY). Arms that stand up and bend across the layers break.
- `L ≥ 5 · t`. Fillet the root.
- Lead-in face 30–45°. The retaining face is 45° to come apart, or 80–90° to stay shut.
  An inward lip in a part printed upright can only be 45° (its underside is an overhang).
- Snap engagement (the bump height) 0.3–0.6 mm.
- Continuous beads (box lids) flex the walls instead of an arm: bead 0.3–0.5 mm, with
  45° faces on both sides.
- Curved arms (arcs around a hub) are cantilevers too: use the arc length as `L`.

## Detents and clicks

Clicks come from a **compliant arm with a round bump** dropping into **notches**, not
from gears forced past each other:

- Bump radius 0.6–1.0 mm, notch radius bump + 0.1, notch depth = engagement + 0.1.
- Engagement 0.3–0.6 mm sets the click depth; the arm width sets the force
  (`F ≈ 3·E·I·δ/L³`, E ≈ 3500 MPa for PLA, `I = h·w³/12`).
- Make the strength a parameter that selects the arm width (soft, medium, firm), and
  add a coupon with one arm of each width.
- Count notches so every arm sits in a notch at once (notches divisible by arms) for a
  crisp click; otherwise the clicks are staggered and softer.

## Gears

Working gears need real involute teeth. Never use decorative stand-ins.

- **Module** ≥ 1.0 (0.8 for small gears), **pressure angle** 20° or 25°. Teeth without
  undercut: at least 17 at 20°, at least 12 at 25°.
- **Backlash** 0.1–0.2 mm per mesh: thin each tooth by half of it at the pitch circle and
  keep the centre distance exact: `a = m · (z1 + z2) / 2`.
- Tip land (tooth top width) ≥ 0.4 mm; lower the addendum if it is thinner.
- Face width 5–10 mm. Inset the bed face (`elephant_foot`) or the teeth bind.
- **Internal gears**: cut with the inverse profile (an external gear with the gap width
  as its tooth width); ring teeth − planet teeth ≥ 10.
- **Planetary**: `ring = sun + 2 · planet`. Equal spacing of N planets needs
  `(sun + ring) % N == 0`. Planet i at angle `θᵢ = i·360/N` is turned by
  `(180 − 180/planet) + θᵢ · (1 + sun/planet)` so every planet meshes. Check that neighbour
  planets clear: `2·a·sin(180/N) > planet tip diameter + 1`.
- Put the gear math in `assert`s and `echo` the ratio. Prove the mesh with
  `check_interference`: it must be clean with the real backlash, and must show a clash on
  every mesh with a small negative backlash (−0.15). That shows the teeth really mesh.
- Herringbone or double-helical teeth for print-in-place gear sets; spur gears for parts
  that are assembled.
- Worked example: the `upio-orbit` project (planetary fidget with detent clicks).

## Hinges, threads, axles

- **Pin hinge** (assembled): pin d ≥ 3 mm, printed lying down if possible; knuckle hole
  running clearance. **Print-in-place hinge**: 0.4 mm gaps, cone bearings (45°) instead of
  a straight pin so the overhang prints.
- **Living hinges**: PETG or PP only, 0.3–0.5 mm thick, printed flat. Never PLA.
- **Printed threads**: M8 and up, pitch ≥ 1.25, 0.2–0.3 mm clearance per side; a
  trapezoid profile with 30° flanks prints better than ISO. Smaller threads: inserts or
  self-tapping.
- **Printed axles** ≥ 4 mm; snap a cap or use a shoulder to hold parts on.

## Test coupons

Any fit the design depends on gets a small `coupon_<feature>` part option:

- A coupon is the real interface geometry, cut down to the smallest print (1–10 g).
- Vary the clearance across copies (0.10, 0.15, 0.20, 0.25, 0.30) and label each one,
  engraved on top, or with 1–5 notches.
- Tell the user which coupon to print first, and which parameter to set from the result.
