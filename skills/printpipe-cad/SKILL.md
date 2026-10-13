---
name: printpipe-cad
description: Design, check and print 3D-printable OpenSCAD (.scad) CAD models through the print-pipeline MCP server (compile_model, get_model, get_assembly, estimate_print, request_approval, print_job). Use when the user asks to make, model, design, change, review or print a 3D part, bracket, enclosure, box, mechanism, gear train, fidget or multi-part assembly for FDM printing, or mentions OpenSCAD, STL, CAD, the print pipeline, the viewer at localhost:8080 or their printer.
---

# Print pipeline CAD

The print-pipeline MCP server turns OpenSCAD source into a print. Every `compile_model`
call stores a new **version** of a **project**, compiles it with OpenSCAD (Manifold
engine), and returns its size, any parameters, the model's own `echo()` output and two
preview images. The user watches the same version live at http://localhost:8080. A print
only starts after the user clicks Approve in that viewer.

Every model made here follows one house format, so any agent or person can open any
project and find the same structure. The format and the quality bar below are not
optional.

## Pick your task

| Task | Read first |
| --- | --- |
| Just an idea, or nothing yet: no project | **Architect mode** below, then the row that fits what you decided |
| New single part (bracket, clip, holder) | [modeling](references/modeling.md), then [single_part.scad](templates/single_part.scad) |
| New object with several printed parts | [modeling](references/modeling.md), [multi-part](references/multi-part.md), [multi_part.scad](templates/multi_part.scad) |
| Anything that moves, snaps, screws, clicks or meshes | [fits](references/fits.md) |
| Assembly timeline for the viewer | [assembly](references/assembly.md) |
| Change an existing project | `get_model` first, then [modeling](references/modeling.md) for the conventions |
| Review or harden a model | [validation](references/validation.md) and [printability](references/printability.md) |
| Estimate, slice, print, monitor, cancel | [tools](references/tools.md) |
| Compile errors, wrong previews, failed prints | [troubleshooting](references/troubleshooting.md) |

MCP clients without these files read the same documents as resources:
`printpipe://skill`, `printpipe://guides/<name>` (modeling, multi-part, assembly, fits,
printability, validation, tools, troubleshooting) and `printpipe://templates/<name>`
(single-part, multi-part).

## The house format, in short

1. **Header comment**: name, purpose, then PARTS (option, count, print orientation),
   ASSEMBLY ORDER, PRINT SETTINGS and TUNING (symptom → parameter).
2. **Customizer parameters** at the top, in groups: `/* [Part] */`, size, fit, features,
   cosmetic. Each one is `name = value; // [min:step:max] What it is, in mm`.
3. **`/* [Hidden] */`**: derived values, the `assembly` declaration, `$fa`/`$fs`.
4. **Checks**: `assert()` for every constraint, and a few `echo("INFO: …")` lines.
5. **Modules**: one per part, drawn in **print orientation, resting on z = 0**.
6. **Dispatcher** at the end: `if (part == …)` for each part, `assembled`, `exploded`,
   `plate_*`, `coupon_*` and `check_*`.

Start from a template rather than a blank file. Details are in
[modeling](references/modeling.md).

## Architect mode: no project needed

The user never has to create or name a project. When they describe something to make, or
only say they want to design something, you are the CAD and 3D-printing architect:

- If there is no idea yet, ask what to make, what it is for and what it must fit or hold, in
  one short message.
- Ask only for what you would otherwise guess, above all real measurements. Choose sensible
  defaults for the rest and say what you chose.
- Decide single part or several, the print orientation of each, and how they join. Share
  that brief in a few lines, then carry on.
- **Name the project yourself**: short, lowercase words joined by `-` (for example
  `pipe-clamp-25mm`; letters, digits, `-` and `_`, at most 64 characters). Check
  `list_projects` first and add `-2`, `-3` if the name is taken. Never write into an
  existing project unless the user asked to change it.
- Your first `compile_model` call with that name creates the project. Then follow the
  workflow below.

MCP clients get this as the `design` prompt, whose only argument (`idea`) is optional.

## Workflow

1. **Pin down the requirements.** Get the real dimensions of anything the part must fit
   (measure, or ask), the material, and the printer (`list_printers` gives the build
   volume). Decide the parts, how each one prints, and how they join.
2. **Name the project** if it is new (see Architect mode), and **write the model** from a
   template, in the house format.
3. **Compile** with `compile_model` (full `scad_code`). Read the summary: size, "fits"
   line, every `INFO`/`CHECK` line and every OpenSCAD `WARNING`. Fix all warnings.
4. **Look at the previews.** Check the overall proportions, the flat base on the bed and
   whether the features you meant to add are there. Don't skip this.
5. **Check every part option.** For multi-part models compile each printable part, then
   `assembled` with an assembly declaration and `get_assembly`, then `check_interference`.
   Change values with `params` and empty `scad_code`; send code only when it changes.
6. **Estimate** with `estimate_print`, and report the time, grams and cost.
7. **Print only when asked**: compile the exact part or plate to print as its own version,
   then `request_approval`. Tell the user to click Approve, then call `print_job`.

## Definition of done

- Compiles with no OpenSCAD warnings and no failed `assert`.
- Every printable option rests flat on z = 0, fits the printer, and needs no supports,
  unless the user agreed to supports.
- Every dimension the user might change is a Customizer parameter with a range and a unit.
- Every mating feature uses a named clearance from [fits](references/fits.md).
- Multi-part: `assembled` has an assembly declaration that `get_assembly` passes, and
  `check_interference` shows no clash, at several positions for moving parts.
- Fits that matter have a `coupon_*` option, printed before the full part.
- You looked at the previews and they match the request.
- The header's PARTS, ASSEMBLY ORDER, PRINT SETTINGS and TUNING sections match the model.

## Hard rules

- You cannot approve a print. Never claim something is printing before `print_job`
  succeeds.
- Never send `assembled`, `exploded`, `section` or `check_*` to the printer: their parts
  overlap. Print parts, plates or coupons.
- Call `get_model` before you edit an existing project, and change it rather than
  rewriting it from memory.
- A long compile can outlast the tool call while the server keeps working. Before you
  compile again, check `list_projects` for the version it was making; don't send the same
  compile twice.
- OpenSCAD only, with no external libraries (BOSL2 and MCAD are not installed). Write
  helpers inline, as the templates do.
