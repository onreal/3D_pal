# Print Pipeline

One Docker container, exposed to an AI agent as an MCP server, that turns a plain-language
request into a printed part on a Bambu Lab A1. The agent writes OpenSCAD, sees its own
renders and estimates; you watch a live 3D preview in the browser and click **Approve**
before anything prints. The host needs only Docker.

Spec: the PRD and the developer setup guide live in the shared Claude Doc
"PRD: AI-Driven OpenSCAD to Bambu A1 Print Pipeline".

## How it runs

One long-running container is the server. It serves:

- the viewer and settings page at <http://localhost:8080>,
- MCP over Streamable HTTP at <http://localhost:8080/mcp>.

Any number of agent sessions connect to it at the same time: Claude Code over HTTP,
Claude Desktop (stdio only) through a small bridge inside the container, several windows
of either. The viewer's **Clients** panel shows each client once, with its connections
grouped underneath. Expand a client to see each connection's transport, tool calls and
project. Follow a client or one connection, end a connection, or use **Clear all clients**
to disconnect every active connection and clear the recent list.
Every tool call in the run log names its session and client.

## Quick start

You need Docker (Docker Desktop on macOS and Windows, Docker Engine on Linux). On Apple
Silicon, turn on "Use Rosetta for x86_64/amd64 emulation" in Docker Desktop settings.

```sh
./printpipe.sh build      # build the image (first time about 10 minutes)
./printpipe.sh up         # start the server; it restarts with Docker
./printpipe.sh mcp-add    # connect Claude Code over HTTP; prints the Claude Desktop config
./printpipe.sh open       # http://localhost:8080
```

Then, in <http://localhost:8080/settings>: add the printer (IP, serial, LAN access
code) and click **Test connection**. There are no slicer profiles to export: the printer
slices with the A1 presets that ship inside Bambu Studio in the image (0.4 mm nozzle,
0.20mm Standard, Bambu PLA Basic, with its list price for costs). Pick another nozzle,
process or filament from the printer's profile lists, which offer every A1 preset
Bambu Studio has. Upload JSON only for presets you changed yourself.
Set **Build plate** on the printer to the plate you use. It sets the bed temperature
(PLA: 65 °C on Textured PEI or High Temp (smooth PEI), 35 °C on Cool Plate). The default
is the Textured PEI Plate the A1 ships with. The printer must be in LAN Only mode with Developer Mode
on, with a microSD card inserted. No printer yet? `./printpipe.sh up --demo` adds a
simulated printer and slicer.

## Up and down: printpipe.sh

| Command | Does |
| --- | --- |
| `./printpipe.sh build` | Builds `print-pipeline:latest` (linux/amd64, runs the unit tests). |
| `./printpipe.sh up [--demo]` | Starts the server in the background (`--restart unless-stopped`) and waits until it is healthy. Running it again is safe; after a `build` it recreates the server on the new image. |
| `./printpipe.sh down` | Stops and removes the server. Connected sessions drop until the next `up`. |
| `./printpipe.sh restart` | `down`, then `up`. |
| `./printpipe.sh status` | Server state, open agent sessions (client, transport, calls, project), printers, whether the image is built. |
| `./printpipe.sh logs [-f]` | The last 200 lines of the server log on the data volume; `-f` follows it. |
| `./printpipe.sh open` | Opens the viewer in the browser. |
| `./printpipe.sh mcp-add [scope]` | Starts the server if needed, registers it with Claude Code over HTTP (scope `user` by default, or `project`, `local`), and prints the Claude Desktop config. |
| `./printpipe.sh mcp-remove [scope]` | Unregisters it from Claude Code. |
| `./printpipe.sh mcp` | For stdio-only MCP clients: starts the server if needed and relays stdio to `/mcp` through `printpipe bridge` inside the container. The bridge stops when the client goes away, even if the client kills the process. |
| `./printpipe.sh skills [DIR]` | Installs the `printpipe-cad` agent skill for Claude Code: a link from `~/.claude/skills` (or `DIR/.claude/skills`) to `skills/printpipe-cad`, so `git pull` updates it. |
| `./printpipe.sh skills-zip [OUT]` | Packages the skill as `printpipe-cad.zip` for claude.ai or Claude Desktop (Settings > Capabilities > Skills). |
| `./printpipe.sh skills-remove [DIR]` | Removes the link `skills` made. |

Settings come from environment variables:

| Variable | Default | Meaning |
| --- | --- | --- |
| `PRINTPIPE_IMAGE` | `print-pipeline:latest` | Image to build and run |
| `PRINTPIPE_PORT` | `8080` | Host port for the viewer and `/mcp` (bound to 127.0.0.1 only) |
| `PRINTPIPE_WORK` | `~/prints` | Host folder with one folder per part |
| `PRINTPIPE_VOLUME` | `print-data` | Docker volume for the database, profiles and logs |
| `PRINTPIPE_CONTAINER` | `print-pipeline` | Name of the server container |
| `PRINTPIPE_DEMO` | `0` | `1` adds a simulated printer when none is configured |

Data lives in the volume and the work folder, so stopping, recreating or rebuilding the
server loses nothing.

## Add the MCP server

Start the server first (`./printpipe.sh up`). The MCP endpoint is
`http://127.0.0.1:8080/mcp` (Streamable HTTP). Every client below joins the same server,
and its connections appear under one client in the viewer.

### Claude Code (HTTP)

```sh
./printpipe.sh mcp-add
```

That runs:

```sh
claude mcp add --transport http print-pipeline -s user http://127.0.0.1:8080/mcp
```

`-s user` makes it available in every project; use `-s project` to share it through
`.mcp.json` in a repository, or `-s local` for this folder only. In a session, `/mcp`
lists the server and its ten tools. Open several Claude Code sessions and the viewer groups
their connections under Claude Code.

### Claude Desktop (stdio, bridged)

Claude Desktop starts local MCP servers over stdio, so it goes through the bridge. Open
Settings → Developer → Edit Config, add this to `claude_desktop_config.json`, and restart
Claude Desktop:

```json
{
  "mcpServers": {
    "print-pipeline": {
      "command": "/path/to/3dpal/printpipe.sh",
      "args": ["mcp"]
    }
  }
}
```

Use the absolute path to `printpipe.sh`; `./printpipe.sh mcp-add` prints the exact
snippet. The file is at `~/Library/Application Support/Claude/claude_desktop_config.json`
on macOS and `%APPDATA%\Claude\claude_desktop_config.json` on Windows. The script starts
the server if it isn't running. The session is listed with transport "stdio bridge".

### Any other MCP client

HTTP clients point at `http://127.0.0.1:8080/mcp`. For stdio-only clients without bash
(for example on Windows without WSL), run the bridge in the server container directly:

```json
{
  "mcpServers": {
    "print-pipeline": {
      "command": "docker",
      "args": ["exec", "-i", "print-pipeline", "printpipe", "bridge"]
    }
  }
}
```

### Check it works

1. `./printpipe.sh status` shows the server running and the image built.
2. In a new session, ask: "List the printers." The agent calls `list_printers`, and the
   client appears in the viewer's Clients panel.
3. Ask: "Make a 20 mm calibration cube, project name test-cube." The cube appears at
   <http://localhost:8080> and the agent shows preview images.

### Troubleshooting

| Symptom | Fix |
| --- | --- |
| `/mcp` says failed or CONNECTION_CLOSED | The server is not running: `./printpipe.sh up`, then reconnect in `/mcp`. `./printpipe.sh status` shows the state. |
| An older registration runs `docker run -i …` and fails | Re-register with `./printpipe.sh mcp-add local` (or `user`). The old one-container-per-session setup conflicted on port 8080. |
| "image print-pipeline:latest not found" | `./printpipe.sh build` |
| A session is stuck, or its client is gone | End its connection in the viewer's Clients panel, or use **Clear all clients** to disconnect everything; idle sessions also close after 12 hours. |
| Port 8080 is taken by another program | `PRINTPIPE_PORT=8090 ./printpipe.sh up` and `PRINTPIPE_PORT=8090 ./printpipe.sh mcp-add`. |
| Very slow on Apple Silicon | Turn on Rosetta emulation in Docker Desktop settings. |
| The printer shows **Status unavailable** | The printer is on the network but its MQTT status service (port 8883) isn't answering; a print keeps running. The A1 takes only a few MQTT clients: close Bambu Studio or Handy if they're connected over LAN. If it persists, restart the printer once it's idle. The server keeps one connection per printer and reads what the printer pushes, so it doesn't add load. |

## Assembly mode

The viewer's **Assembly** tab adds a model's parts one by one along a timeline: play,
pause, step, scrub, change speed, show faint outlines of the parts still to come
("Ghosts"), see through housings ("X-ray"), click a step to jump to it, and reorder steps
(kept per project in the browser).

For named, ordered steps the model declares its build order and draws one step on its
own when asked. The agent knows this convention; for an existing model ask it to "add
assembly steps":

```openscad
/* [Hidden] */
assembly = [["cap_bottom", "Bottom cap"], ["ring", "Ring"], ["planets", "Planets"]]; // build order
assembly_only = "";  // the pipeline sets this to render one step alone
module step(id) { if (assembly_only == "" || assembly_only == id) children(); }

module assembled() {
  step("cap_bottom") color("orange") cap_bottom();
  step("ring")       color("orange") ring();
  step("planets")    color("dimgray") for (i = [0:3]) planet_at(i);
}
```

Each step is rendered separately in its assembled position (a few at once, with the
Manifold engine), with its colour, and identical copies are counted (Planets ×4). If the
model has a `part` parameter with an `assembled` option, that option is used. A model
without the declaration still works: its assembled render is split into separate pieces,
bottom to top, though parts that touch show as one. Results are cached per version.

## Agent skills: the printpipe-cad bundle

Every model made through the pipeline follows one house format, so any agent or person
can open any project and find the same structure: a header with the parts list, assembly
order, print settings and tuning table; Customizer parameters in fixed groups, with
clearances per side in `[Fit]`; `assert` and `echo("INFO: …")` checks; one module per
part in print orientation on z = 0; and a `part` dispatcher with `assembled`, `exploded`,
`plate`, `coupon_*` and `check_*` options.

The rules live in one place, [`skills/printpipe-cad`](skills/printpipe-cad), in the Agent
Skills format, and reach agents three ways:

- **Claude Code skill**: `./printpipe.sh skills` links it into `~/.claude/skills`. Claude
  loads it whenever a task is about designing or printing with the pipeline.
- **claude.ai / Claude Desktop skill**: `./printpipe.sh skills-zip`, then upload the zip.
- **MCP**: the server embeds the same files (package `skills`) and serves them to any
  client as resources and prompts. Its instructions tell agents to read `printpipe://skill`
  first.

| File | MCP resource | Covers |
| --- | --- | --- |
| `SKILL.md` | `printpipe://skill` | Workflow, house format, definition of done, which guide to read |
| `references/modeling.md` | `printpipe://guides/modeling` | The file layout, parameter conventions, part modules, dispatcher |
| `references/multi-part.md` | `printpipe://guides/multi-part` | Splitting into parts, orientation, joints, placements, plates, BOM |
| `references/assembly.md` | `printpipe://guides/assembly` | The assembly timeline declaration |
| `references/fits.md` | `printpipe://guides/fits` | Clearances, hardware holes, snap fits, detents, gears, hinges, threads, coupons |
| `references/printability.md` | `printpipe://guides/printability` | Walls, overhangs, bridges, first layer, strength, holes, text, materials |
| `references/validation.md` | `printpipe://guides/validation` | Asserts, echo output, previews, interference checks, review checklist |
| `references/tools.md` | `printpipe://guides/tools` | Every MCP tool, when to use it, mistakes to avoid |
| `references/troubleshooting.md` | `printpipe://guides/troubleshooting` | Compile errors, slow compiles, wrong previews, print problems → parameters |
| `templates/single_part.scad` | `printpipe://templates/single-part` | A complete one-part model (wall hook) |
| `templates/multi_part.scad` | `printpipe://templates/multi-part` | A complete multi-part model (snap box): assembly, plate, coupon, interference check |

| Prompt | Arguments | Task |
| --- | --- | --- |
| `design` | `idea` (optional) | **Architect mode.** Start from an idea, or from nothing. Claude asks what it needs, decides single part or assembly, names and creates the project itself, and takes it to an estimate |
| `design_part` | `request`, `project` (optional) | A single part in the house format, checked and estimated |
| `design_assembly` | `request`, `project` (optional) | A multi-part object: interfaces, assembly timeline, interference check, coupons, plate |
| `add_assembly_timeline` | `project`, `order` | Declare the build order for the viewer's Assembly tab |
| `review_model` | `project` | Audit a project against the checklist; report problems with fixes |
| `tune_fit` | `project`, `problem` | Adjust clearances after a test print and recompile the coupon |
| `print_model` | `project`, `part` | Pick a printable option, estimate, request approval, print |

In Claude Code the prompts are slash commands, e.g. `/mcp__print-pipeline__review_model
upio-orbit`, and resources can be attached with `@print-pipeline:printpipe://guides/fits`.

**You never need a project to start.** Run `/mcp__print-pipeline__design`, with or without an
idea, or just describe what you want in chat. Claude names the project itself (checking
`list_projects` so it never writes into an existing one), and its first `compile_model` call
creates it. Only the prompts that work on an existing model (review, tune, timeline, print)
take a project name.

`compile_model` also reports each model's `echo()` lines and OpenSCAD warnings under
"Model output", so the checks a model prints reach the agent. The `get_assembly` tool
builds and reviews a version's timeline.

Tests keep the bundle honest: every file is served, every relative link resolves, the
front matter is valid, the templates follow the format, and (in the integration stage)
every template option compiles with the real OpenSCAD with no warnings, sits flat on the
bed, passes its interference check, and fails it with a negative clearance.

## What the agent gets

| Tool | Does |
| --- | --- |
| `compile_model` | Writes `model.scad` as a new version, compiles it with OpenSCAD, returns preview PNGs and the size. Empty code + `params` changes parameters only. |
| `get_model` | A version's code and its Customizer parameters. |
| `list_projects` | Projects in the workspace. |
| `estimate_print` | Slices one or several versions; time, grams, metres, layers, cost. Cached by inputs. Never creates a job. |
| `slice_model` | One slice with explicit supports, layer height or infill. |
| `request_approval` | Slices and puts an Approve button in the viewer. Sends nothing. |
| `print_job` | Uploads and starts an approved job. Fails with "Not approved yet" before your click. Prints once. |
| `list_printers`, `printer_status`, `cancel_print` | Printers, live state, stop. |
| `get_assembly` | The version's assembly timeline: steps in build order, counts, colours, problems to fix. |

There is no approve tool: only your click in the viewer approves a print, and the setting
cannot be turned off.

### Rendering

Changing a parameter renders nothing: the panel marks the edits "Not rendered yet" until you
press **Render 3D** (or Enter in a field), which compiles them as one new version. **Reset**
puts back the values of the version shown. While any render runs, from the viewer, the
agent's `compile_model` or a restore, a banner over the model says who started it and for
how long, and the model shown dims: it is still the previous version. The banner then shows
the new version, or the error.

### Printing from the viewer

You don't need the agent to print. **Print…** in the top bar slices the version on screen
for the selected printer and shows the job with its time, filament and cost. **Approve and
print** sends it. **Approve only** leaves it for the agent's `print_job`, and **Send to
printer** sends an approved job later. A job prints once: **Print again** on a printed job
slices a new job for the same version, which you approve the same way.

**One part of a model.** A model with a `part` parameter (for example `cap_top`) prints one
part at a time. Choose the part under Parameters, which makes a new version, then **Print…**.
The job panel shows only the job for the version on screen. **Discard** drops a job you no
longer want, so it can't be approved or sent.

**Following a print.** While the printer runs a job sent from here, the status bar shows
**Watch**. It opens the Toolpaths tab on the exact file the printer is running, with **Follow
print** on. You see the layers printed so far, the current layer marked, and the layer,
percent and time left as the printer reports them (every 3 s). The printer reports its
layer but not the nozzle's position, so the path within the current layer is an estimate,
paced by how long the previous layer took. It corrects itself at each new layer. Moving the
layer slider stops following. After a server restart, the print is found again from the
name the printer reports.

**"MQTT verification failed, update studio to include the network plugin"** on the
printer's screen, or "printer refused the command … Developer Mode" in the viewer: since
Bambu's 2025 authorization firmware, the A1 takes print commands only from Bambu's own
software unless **Developer Mode** is on. On the printer: Settings, turn on **LAN Only
Mode**, then turn on **Developer Mode**. The access code may change; update it in Settings.
Then press **Try again** on the job. The file is already on the SD card, and a refused job
is not counted as printed. Developer Mode turns off Bambu's cloud and the Handy app for
this printer.

## Layout

```
cmd/printpipe/          one binary: viewer, API and MCP over HTTP on :8080; printpipe bridge
internal/store/         SQLite (modernc.org/sqlite), embedded migrations
internal/config/        settings, masked secrets, profile uploads, import/export
internal/project/       one folder per part, numbered versions
internal/scad/          OpenSCAD compile, preview render, Customizer parameters
internal/slicer/        Slicer interface; slicers/bambustudio and slicers/fake
internal/printer/       Printer interface, registry, printertest contract suite
internal/printers/      bambu (FTPS + MQTT) and fake drivers
internal/estimates/     estimate cache with shared runs
internal/jobs/          approvals bound to the file's SHA-256
internal/pipeline/      the service behind every tool and endpoint
internal/mcpserver/     MCP tools, resources, prompts and session tracking
skills/printpipe-cad/   the agent skill: SKILL.md, references/, templates/ (embedded by package skills)
internal/sessions/      open agent sessions: client, transport, activity
internal/bridge/        stdio-to-/mcp relay for stdio-only clients
internal/web/           HTTP API, SSE, localhost and origin checks, the embedded viewer
web/                    Vite + TypeScript + three.js viewer and settings page
e2e/                    drives the real image over MCP
```

## Tests

```sh
go test ./...                                    # Go unit tests, no external tools
cd web && npx vitest run && npx playwright test  # frontend unit and end-to-end
docker build --platform linux/amd64 --target integration .   # real OpenSCAD and Bambu Studio
docker build --platform linux/amd64 -t print-pipeline:dev . && go test -tags e2e -v ./e2e/
```

The image build runs the Go and Vitest suites; a failing test fails the build.

## Development without Docker

```sh
cd web && npm ci && npm run build && cd ..
cp -r web/dist/* internal/web/static/
go run ./cmd/printpipe -demo -data ./.data -work ./.work
# viewer on http://localhost:8080, MCP on http://localhost:8080/mcp;
# npm run dev in web/ proxies /api to it
```

OpenSCAD must be on the PATH (or set `OPENSCAD`) to compile outside Docker.
