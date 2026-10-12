# Printability (FDM, 0.4 mm nozzle, 0.2 mm layers)

These rules are for a Bambu Lab A1 class printer with PLA or PETG. Other printers: read
their build volume from `list_printers`. Rules are written as numbers you can put into
`assert()`.

## Walls and small features

| Feature | Minimum | Good |
| --- | --- | --- |
| Wall, cosmetic | 0.8 mm (2 lines) | 1.2 mm |
| Wall, structural | 1.6 mm (4 lines) | 2.0–3.0 mm |
| Wall thickness step | multiples of 0.4 mm | |
| Detail in XY (rib, pin, letter stroke) | 0.5 mm | 0.8 mm |
| Detail in Z (step, emboss, engrave) | 0.4 mm (2 layers) | 0.6 mm |
| Vertical pin diameter | 3 mm | 5 mm or more, or print it lying down |
| Floor or roof | 0.8 mm (4 layers) | 1.2 mm |
| Gap that must stay open | 0.3 mm in XY, 0.2 mm in Z | 0.4 mm |

## Overhangs and bridges

- Faces up to **45° from vertical** print without supports. On the bed side of a part, use
  a **45° chamfer**, not a fillet: a round edge facing down overhangs at its bottom.
- **Horizontal holes** (axis parallel to the bed): 8 mm or less print fine as circles. Bigger
  ones get a **teardrop** or flat-topped top, so the roof stays at 45°.
- **Bridges** (flat roofs over a gap, with support at both ends): up to 10 mm print clean,
  up to 25 mm print with some sag. A roof over a hole's countersink needs a
  sacrificial layer: one 0.2 mm skin that you drill out, or a stepped bridge.
- An overhang that only goes outwards as the part rises is fine at any angle. Check the
  direction, not just the angle.
- **Inward lips** in a part printed upright (a snap ledge inside a ring) need a 45° underside.

## The first layer

- The first layer squashes outwards (**elephant foot**) by about 0.1–0.2 mm. Either chamfer
  the bottom edges 0.3–0.5 mm at 45°, or inset the first 0.2 mm by `elephant_foot` (a
  `[Fit]` parameter, default 0.2). This matters on gears, hole edges and mating faces that
  touch the bed.
- A vertical hole that starts on the bed gets a 0.4 mm chamfer at its mouth.
- Large flat parts: round the corners (at least 3 mm radius) so they don't lift; tell
  the user to add a brim for parts over 150 mm.

## Strength and orientation

- The layer bond is the weak direction. Clips, hooks and arms must bend **in the XY plane**.
- Screw bosses stand vertical, with at least 1.6 mm of wall around the hole.
- Stress concentrates in sharp inside corners. Add a fillet or chamfer of at least the
  wall thickness where an arm meets a body.
- Strength comes mostly from walls (perimeters), not infill: for a strong part, specify
  4–6 walls in PRINT SETTINGS rather than more infill.

## Holes print small

Vertical holes print about 0.1–0.2 mm under size, horizontal ones a bit more. Use the hole
table in [fits](fits.md), or give the hole a clearance parameter. For a precise round hole,
use enough facets (`$fs = 0.3`) so the polygon doesn't sit inside the circle.

## Text and logos

- Put text on **top faces** or **side faces**. Text on the bed face only works as engraved
  text at least 0.4 mm deep, and it comes out mirrored unless you mirror it in the model.
- Embossed or engraved by **0.4–0.6 mm**. Cap height **≥ 4 mm** on top faces and ≥ 5 mm on
  vertical faces. Use a bold sans font (`"Liberation Sans:style=Bold"`; the pipeline
  image also has `"DejaVu Sans:style=Bold"`).
- Raised text inside a shallow recess stays flush with the surface and survives handling.

## Materials

| Material | Use | Notes |
| --- | --- | --- |
| PLA | Most parts, gears, fidgets | Stiff; creeps under constant load; softens at ~55 °C |
| PETG | Snap fits, outdoor, living hinges | Tougher, strings more: +0.05 mm on clearances |
| TPU | Grips, bumpers, gaskets | Flexible: no bridges, generous walls, slow |
| ABS/ASA | Heat, UV | Warps on an open printer like the A1: avoid unless enclosed |

## Size

- Every printable option must fit the build volume with a 5 mm margin. The compile summary
  says whether it fits the default printer.
- Split parts bigger than the bed (see [multi-part](multi-part.md)).
