# Print Pipeline

One Docker container, exposed to an AI agent as an MCP server, that turns a plain-language
request into a printed part on a Bambu Lab A1. The agent writes OpenSCAD, sees its own
renders and estimates; you watch a live 3D preview in the browser and click **Approve**
before anything prints. The host needs only Docker.

Spec: the PRD and the developer setup guide live in the shared Claude Doc
"PRD: AI-Driven OpenSCAD to Bambu A1 Print Pipeline".

## Quick start

```sh
docker build --platform linux/amd64 -t print-pipeline:1.0 .
docker volume create print-data
mkdir -p ~/prints
claude mcp add print-pipeline -- docker run -i --rm --platform linux/amd64 \
  -p 127.0.0.1:8080:8080 -v print-data:/data -v "$HOME/prints:/work" print-pipeline:1.0
```

Open <http://localhost:8080/settings>, add the printer (IP, serial, LAN access code),
upload the A1 machine, process and filament profiles exported from Bambu Studio, and click
**Test connection**. The printer must be in LAN Only mode with Developer Mode on, with a
microSD card inserted.

No printer yet? Add `-e PRINTPIPE_DEMO=1` to the `docker run` arguments: a simulated
printer and slicer are configured when none exists.

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

There is no approve tool: only your click in the viewer approves a print, and the setting
cannot be turned off.

## Layout

```
cmd/printpipe/          one binary: MCP on stdio + HTTP on :8080
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
internal/mcpserver/     MCP tools
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
go run ./cmd/printpipe -mcp=false -demo -data ./.data -work ./.work
# viewer on http://localhost:8080; npm run dev in web/ proxies /api to it
```

OpenSCAD must be on the PATH (or set `OPENSCAD`) to compile outside Docker.
