# Models with several parts

Use this when an object has more than one printed body: boxes with lids, enclosures,
mechanisms, kits. Start from `printpipe://templates/multi-part`.

## Deciding the parts

Split an object into parts when one of these is true:

- **It can't print well in one piece.** Some face would need supports, or a strong
  direction would end up across the layer lines.
- **It has to move.** Gears, hinges, sliders and caps are separate parts, unless you are
  designing print-in-place on purpose (see [fits](fits.md)).
- **It is bigger than the bed.** Split it along a flat face and join it with a dovetail,
  pins or screws.
- **It needs another material or colour.**

Keep the number of parts as low as the function allows: every joint adds a tolerance and
an assembly step.

## Print orientation, per part

Pick the orientation for each part before you model its details:

1. The biggest flat face goes on the bed.
2. No face steeper than 45° from vertical points down, unless it is a short bridge.
3. Loads run along the layers, not across them. Snap arms, flexures and clips lie flat,
   so they bend in XY.
4. Faces people see or that must be precise go up or on the side; the bed face is
   slightly flared (elephant foot).

Each part module draws its part in that orientation, resting on z = 0. Write the
orientation into the header's PARTS table.

## Interfaces between parts

- **Define each interface once** in the hidden section (`lip_w`, `pin_d`, `bead_h`) and
  build both sides from those names. Apply the clearance on one side only: usually the
  hole, socket or outer part gets `+ clearance`.
- **Pick the joint by function:**

| Joint | Use for | Notes |
| --- | --- | --- |
| Snap bead or cantilever | Lids, covers, caps | Releasable if the retaining face is 45°; see [fits](fits.md) |
| Press-fit pin or peg | Locating, light loads | 0.05–0.1 mm interference per side; chamfer the entry |
| Keyed shaft (D, hex) | Torque | Hex or D-flat with `fit` clearance |
| Screw into insert or nut trap | Strong, can come apart | Hardware sizes in [fits](fits.md) |
| Dovetail or puzzle joint | Splitting big parts | 0.15 mm per side; run it along the layers |
| Twist lock (bayonet) | Round lids | Add a detent bump so it stays shut |

- Every joint gets a **lead-in**: a 0.5–1 mm chamfer on the entering edge.
- Moving parts get an **axial gap** of at least one layer (0.2 mm) between stacked faces.

## Placing parts in the assembly

Part modules are in print orientation, so `assembled()` places each one with its own
transform, kept inside the `step()` (see [assembly](assembly.md)):

```openscad
module assembled(explode = 0) {
  step("base") color("#ff7a1a") base();
  step("lid")  color("#2e2e33") translate([0, 0, base_h + lid_t + explode]) rotate([0, 180, 0]) lid();
}
```

- A part printed upside down is turned over with `rotate([0, 180, 0])` (or
  `[180, 0, 0]`), then lifted to its real height. **Never place a part with `mirror()`.** A
  mirror makes the mirror image, a part that doesn't exist: text reads backwards, threads
  change hand, and asymmetric features end up on the wrong side.
- Moving parts take their state from a parameter (`spin`, `open_angle`), so `assembled`
  and `check_interference` can be compiled at several positions.
- `exploded` reuses `assembled` with an `explode` distance: each part moves along its
  assembly direction by a multiple of it.

## Plates

A `plate` option arranges parts that print together:

- Space parts at least 5 mm apart, all resting on z = 0, and keep the whole plate within
  the printer's bed (`list_printers`), leaving a 5 mm margin.
- Parts that need different settings (supports, infill, material) go on separate plates.
- Copies that the object needs several of (4 planets, 6 feet) go on the same plate.

## Bill of materials

The header's PARTS table lists every option you print, how many copies, the orientation
and the material. Hardware that isn't printed (screws, inserts, magnets, bearings) gets its
own line, with the exact size and length.

## Checks for multi-part models

1. Compile each printable part option and look at its previews.
2. Compile `assembled` and run `get_assembly`: every step renders, counts are right, the
   order is the real build order.
3. Compile `check_interference`. A clean result is only the 2 mm marker cube, so the
   summary says `Size 2.0 × 2.0 × 2.0 mm`. Anything bigger is a clash. For moving parts,
   repeat at several positions.
4. Add `coupon_*` options for the fits that matter, and tell the user to print those first.
