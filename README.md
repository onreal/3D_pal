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
of either. The viewer's **Sessions** panel lists every open session with the client that
holds it (name, version, transport, address, user agent), its tool calls and the project
it works on. Follow one session or all of them, and end a session from there if needed.
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
code), upload the A1 machine, process and filament profiles exported from Bambu Studio,
and click **Test connection**. The printer must be in LAN Only mode with Developer Mode
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
and each appears as its own session in the viewer.

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
lists the server and its ten tools. Open several Claude Code sessions and each shows up
separately in the viewer.

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
   session appears in the viewer's Sessions panel with its client name.
3. Ask: "Make a 20 mm calibration cube, project name test-cube." The cube appears at
   <http://localhost:8080> and the agent shows preview images.

### Troubleshooting

| Symptom | Fix |
| --- | --- |
| `/mcp` says failed or CONNECTION_CLOSED | The server is not running: `./printpipe.sh up`, then reconnect in `/mcp`. `./printpipe.sh status` shows the state. |
| An older registration runs `docker run -i …` and fails | Re-register with `./printpipe.sh mcp-add local` (or `user`). The old one-container-per-session setup conflicted on port 8080. |
| "image print-pipeline:latest not found" | `./printpipe.sh build` |
| A session is stuck, or its client is gone | End it in the viewer's Sessions panel; idle sessions also close after 12 hours. |
| Port 8080 is taken by another program | `PRINTPIPE_PORT=8090 ./printpipe.sh up` and `PRINTPIPE_PORT=8090 ./printpipe.sh mcp-add`. |
| Very slow on Apple Silicon | Turn on Rosetta emulation in Docker Desktop settings. |

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
internal/mcpserver/     MCP tools and session tracking
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
