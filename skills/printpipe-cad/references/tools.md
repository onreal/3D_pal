# The print pipeline's tools

What each MCP tool does, when to use it, and the mistakes to avoid. The user watches every
version, slice and job in the viewer at http://localhost:8080.

## Projects and versions

A **project** is a folder of numbered **versions**. Every `compile_model` call makes a
new version, even when only a parameter changed, so versions are cheap. Never edit an old
version: compile a new one.

## Modelling

### `compile_model(project, scad_code?, params?, estimate?)`

Writes the code as a new version, compiles it, renders previews and reports:

- the size, and whether it fits the default printer (`DOES NOT FIT` means make it smaller
  or split it);
- the parameter values used;
- **Model output**: every `echo()` line and OpenSCAD warning;
- `Assembly: N steps`, or a tip to add steps if there is an `assembled` option;
- the preview images (iso, front) of the selected `part` option.

Use it like this:

- **New code, or code that changed**: send the complete file in `scad_code`.
- **Only values change**: empty `scad_code` and `params`, e.g. `{"part": "lid"}` or
  `{"wall": 2.4}`. That reuses the latest version's code.
- `estimate: true` also slices it for the default printer: use it on printable options
  only.
- The STL of a version is the `part` option it was compiled with. To print a part, compile
  that part as its own version.

Compiles of one project run one at a time. A big model can outlast your tool call while
the server keeps going: check `list_projects` before sending the same compile again.

### `get_model(project, version?)`

The code and Customizer parameters (values, ranges, options) of a version, the latest if
none is given. **Call it before you change an existing project**, and edit that code.

### `list_projects()`

Every project and its latest version. Use it to find a project's name, or to see whether
a compile you lost track of finished.

### `get_assembly(project, version?)`

Builds the assembly timeline of a version and reviews it: steps in build order, counts
("Planets ×4"), colours, positions and any problems. Run it after compiling `assembled`
with an assembly declaration ([assembly](assembly.md)). Fix every problem it reports.

## Estimating and slicing

### `list_printers()`

Configured printers, their model, build volume, job format and the default. Read the
build volume before designing anything big. With no printer configured, estimates, slices
and prints aren't available: ask the user to add one in the viewer.

### `estimate_print(project, versions?, printer_id?, layer_height?, infill?, supports?, support_type?)`

Slices one or more versions and returns time, grams, metres, layers and cost. Results are
cached, so comparing variants is cheap: `versions: [3, 4, 5]`. It never creates a job.

### `slice_model(project, version?, supports?, support_type?, layer_height?, infill?)`

One slice with explicit options; the toolpaths appear in the viewer. Use it to see whether
a part needs supports (slice with `supports: false` and look), or to show the user layers.

## Printing

### `request_approval(project, version?, printer_id?, …slice options)`

Slices the version and puts an **Approve** button with the estimate in the viewer.
Nothing is sent to the printer. Before calling it:

- the version is a printable part, plate or coupon, never `assembled`, `exploded`,
  `section` or `check_*`;
- the summary said it fits the printer;
- the user has asked to print.

Then tell the user to check the viewer and click Approve, and wait for them.

### `print_job(job_id)`

Uploads and starts an approved job. It fails with "not approved yet" until the user has
clicked Approve; then ask them again rather than retrying in a loop. A job prints once.
Say it is printing only after this call succeeds.

### `printer_status(printer_id?)`

State (idle, printing, paused, finished, failed, offline) and progress. Use it when the
user asks how the print is doing.

### `cancel_print(printer_id?)`

Stops the current print. **Only when the user asks to stop**: it can't be undone.

## Guides and prompts

- Resources: `printpipe://skill` (overview), `printpipe://guides/<name>`
  (modeling, multi-part, assembly, fits, printability, validation, tools,
  troubleshooting), `printpipe://templates/single-part`, `printpipe://templates/multi-part`.
- Prompts: `design_part`, `design_assembly`, `add_assembly_timeline`, `review_model`,
  `tune_fit`, `print_model`. Each one gives a step-by-step task with the relevant guides.
