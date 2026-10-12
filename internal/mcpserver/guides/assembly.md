# Assembly timeline: how to add one to a model

The viewer's **Assembly** tab plays a model's parts into place one by one, in build order.
You give it that order by declaring the steps in the `.scad` file. Then the pipeline
renders each step on its own, in its assembled position, and the user watches the parts
come together.

## The convention

Put this at the top level of the model, near the other parameters:

```openscad
/* [Hidden] */
// Build order: the first step goes on the bed first.
assembly = [
  ["base",     "Base plate"],
  ["bearings", "Bearings (not printed)"],
  ["gears",    "Gears"],
  ["lid",      "Lid"],
];
assembly_only = "";   // the pipeline sets this to one ID to render that step alone
module step(id) { if (assembly_only == "" || assembly_only == id) children(); }
```

Then wrap every part of the assembled view in `step("<id>")`, outside its `color()` and
every `translate()`/`rotate()` that places it:

```openscad
module assembled() {
  step("base")     color("orange")   base();
  step("bearings") color("silver")   for (p = bearing_spots) translate(p) bearing();
  step("gears")    color("dimgray")  translate([0, 0, base_h]) rotate(gear_angle) gears();
  step("lid")      color("orange")   translate([0, 0, base_h + gap]) lid();
}
```

If the model has a `part` parameter, keep an `"assembled"` option that calls
`assembled()`. The pipeline picks that option for the timeline. The other options stay
as they are: printable parts laid flat on the bed.

```openscad
part = "assembled"; // [assembled, base, gears, lid, plate]
if (part == "assembled") assembled();
else if (part == "base") base();
...
```

Only two names are fixed: `assembly` and `assembly_only`. Rename `step()` if the model
already has a module called that.

## Rules

1. **Order is physical build order.** Ask yourself what goes in first, and put a part
   before anything that would block it. Parts that are pressed in from the top come after
   the part they go into. The timeline drops each part in from above.
2. **One step per thing the user puts in.** Identical copies inserted together (four
   planets, six screws) are one step; the viewer counts them ("Planets ×4"). Copies
   inserted at different moments are separate steps.
3. **Assembled positions, real clearances.** Each step draws its part exactly where it
   sits in the finished object, with the clearances you designed. Don't shift parts to
   make them look nicer.
4. **A colour per step.** Give each step its own `color()`, so the user can tell them
   apart. Use grey or silver for hardware.
5. **Hardware counts.** Screws, bearings, magnets and shafts that aren't printed can be
   steps too. Draw a simple stand-in and say "(not printed)" in the label.
6. **Unique IDs, readable labels.** IDs are short snake_case; labels are what the user
   reads in the build order list.
7. **Every step must draw something.** With `assembly_only` set to its ID, a step that
   renders nothing is reported as failed.

## Check your work

1. Compile with `compile_model`. The summary says "Assembly: N steps".
2. Call `get_assembly` for the project. Check that every step rendered, that the counts
   match what you expect (Planets ×4), and that the order makes sense from bottom to top.
   Fix any step it reports as failed.
3. Tell the user to open the **Assembly** tab and press **Play**.

## Common mistakes

- `step()` inside a module that the printable `part` options also use: the plate views
  would then hide parts whenever `assembly_only` is set. Wrap the calls in `assembled()`,
  not inside the part modules.
- A `translate()` outside `step()`: that's harmless, but then the step and its offset
  are split across lines, which is easy to break later. Keep the placement inside the step.
- Forgetting `assembly_only = "";`: the pipeline reports that the declaration is
  incomplete.
- Two steps with the same ID: only the first is used.
- Without a declaration, the timeline falls back to splitting the assembled model into
  separate pieces. Parts that touch then show as one, with no names. That's the cue to add
  the declaration.
