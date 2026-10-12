# Validation: proving the model works before it prints

A model is checked in four layers. Each one finds mistakes the others miss.

## 1. Self-checks in the code

- **`assert(condition, message)`** for every rule the geometry depends on: minimum walls,
  build volume, gear constraints, snap strain limits, parts that must clear each other,
  parameter combinations that make no sense. A failing assert stops the compile and shows
  the message to the agent and the user. Write the message as the fix:
  `"wall must be at least 1.2 mm"`, not `"bad wall"`.
- **`echo("INFO: …")`** for the numbers a person wants after each change: outer size, gear
  ratio, click force, strain, mass hint. The compile summary prints every echo line under
  "Model output". Keep it under 10 lines; prefix warnings you raise yourself with
  `WARNING:` in the text.
- Fix every OpenSCAD `WARNING` in the summary (unknown variables, ignored arguments,
  missing fonts). A warning usually means a feature silently disappeared.

## 2. Look at the previews

`compile_model` returns an iso and a front view of the selected part option. For each:

- Is it the object that was asked for, in sensible proportions?
- Does every printable option sit flat on the bed, with its biggest face down?
- Are the features you meant to add there: holes, text, snaps, teeth?
- In the front view: any part floating above the bed, or hanging down?
- For internal features, compile `section` (the assembled view cut in half).

## 3. Geometric checks with views

Add `check_*` options that compute problems instead of showing the model:

```openscad
// Every pair of parts that could touch; anything left is a clash.
module check_interference() {
  intersection() { base_placed(); lid_placed(); }
  intersection() { base_placed(); divider_placed(0); }
  // ... every pair that could touch
  cube(2, center = true);   // marker: an empty model would fail to compile
}
```

- A clean result is just the marker, so the summary says `Size 2.0 × 2.0 × 2.0 mm`.
  Anything bigger is a clash; look at the preview to find it.
- Use the real placements from `assembled()` (factor them into `*_placed()` modules, or
  call the same transforms), without colours and without `step()`.
- Parts that rest on each other by design (a lid on a rim, a gear on its washer) touch
  face to face, and the Manifold engine reports that as a paper-thin overlap. Lift the
  resting part by `eps` in the check: `lid_placed(eps)`.
- For moving parts, compile the check at several positions (`spin = 0, 7, 13.3, 25`).
- **Prove that the check can fail.** Set a clearance slightly negative and compile again:
  the clash must now appear. A check that can't fail proves nothing.
- Leave cosmetic detail out of checks (knurls, text) when it is slow and not a mating face.

## 4. Assembly, estimate and coupons

- `get_assembly` after compiling `assembled`: every step renders, counts match, order is
  the build order, and no problems are reported.
- `estimate_print` for each printable option: compare the time and grams to what the part
  should be. A 10 g clip that estimates 80 g means hidden solid geometry or wrong units.
- **Coupons** (`coupon_<feature>`): tell the user to print them first and which parameter
  each result sets. The first full print should be one that works.

## Review checklist

Use this when you review a project, or before you call it done:

1. Header present and true: PARTS, ASSEMBLY ORDER, PRINT SETTINGS, TUNING.
2. All tunable values are Customizer parameters with ranges and units; clearances are in
   `[Fit]`, per side.
3. Compiles with no OpenSCAD warnings; asserts cover the constraints; a few INFO lines.
4. Every part option: flat on z = 0, fits the bed, no unsupported overhang, walls ≥ 1.2 mm,
   bed-face edges chamfered or inset.
5. Every mating pair: named clearance, lead-in chamfer, interference check clean, and it
   fails with a negative clearance.
6. Moving parts: checked at several positions; clicks and snaps have their strain
   echoed and asserted.
7. Multi-part: assembly declaration passes `get_assembly`; `plate` fits the bed.
8. Coupons exist for the critical fits.
9. Estimate looks right for the part.
