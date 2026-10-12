# Print Pipeline

One Docker container, exposed to an AI agent as an MCP server, that turns a plain-language
request into a printed part on a Bambu Lab A1. The agent writes OpenSCAD, sees its own
renders and estimates; you watch a live 3D preview in the browser and click **Approve**
before anything prints. The host needs only Docker.

Spec: the PRD and the developer setup guide live in the shared Claude Doc
"PRD: AI-Driven OpenSCAD to Bambu A1 Print Pipeline".

## Quick start

You need Docker (Docker Desktop on macOS and Windows, Docker Engine on Linux). On Apple
Silicon, turn on "Use Rosetta for x86_64/amd64 emulation" in Docker Desktop settings.

```sh
./printpipe.sh build      # build the image (first time about 10 minutes)
./printpipe.sh up         # start the viewer and settings page on their own
./printpipe.sh open       # http://localhost:8080
./printpipe.sh mcp-add    # connect Claude Code; prints the Claude Desktop config too
```

Then, in <http://localhost:8080/settings>: add the printer (IP, serial, LAN access
code), upload the A1 machine, process and filament profiles exported from Bambu Studio,
and click **Test connection**. The printer must be in LAN Only mode with Developer Mode
on, with a microSD card inserted. No printer yet? `./printpipe.sh up --demo` adds a
simulated printer and slicer.

Start a Claude session and ask for a part. The session starts its own container, which
also serves the viewer, and removes it when the session ends.

## Up and down: printpipe.sh

| Command | Does |
| --- | --- |
| `./printpipe.sh build` | Builds `print-pipeline:latest` (linux/amd64, runs the unit tests). |
| `./printpipe.sh up [--demo]` | Starts the viewer and settings page without an agent, in the background, and waits until it is healthy. Running it twice is safe. |
| `./printpipe.sh down [--all]` | Stops the viewer. `--all` also stops containers of agent sessions still running. |
| `./printpipe.sh restart` | `down`, then `up`. |
| `./printpipe.sh status` | Running containers, viewer health, configured printers, whether the image is built. |
| `./printpipe.sh logs [-f]` | The last 200 lines of the server log on the data volume; `-f` follows it. |
| `./printpipe.sh open` | Opens the viewer in the browser. |
| `./printpipe.sh mcp-add [scope]` | Registers the server with Claude Code (scope `user` by default, or `project`, `local`) and prints the Claude Desktop config. |
| `./printpipe.sh mcp-remove [scope]` | Unregisters it from Claude Code. |
| `./printpipe.sh mcp` | What the MCP client runs. Stops the standalone viewer so port 8080 is free, then runs the container with `-i` and serves MCP on stdio. |

Settings come from environment variables:

| Variable | Default | Meaning |
| --- | --- | --- |
| `PRINTPIPE_IMAGE` | `print-pipeline:latest` | Image to build and run |
| `PRINTPIPE_PORT` | `8080` | Host port for the viewer (bound to 127.0.0.1 only) |
| `PRINTPIPE_WORK` | `~/prints` | Host folder with one folder per part |
| `PRINTPIPE_VOLUME` | `print-data` | Docker volume for the database, profiles and logs |
| `PRINTPIPE_DEMO` | `0` | `1` adds a simulated printer when none is configured |

Only one container can hold the port. `up` refuses while an agent session runs, and an
agent session stops the standalone viewer when it starts. Data lives in the volume and
the work folder, so stopping or rebuilding the container loses nothing.

## Add the MCP server

The server speaks MCP over stdio: the client starts the container with `docker run -i`
for each session, and the container exits when the session ends. Build the image first
(`./printpipe.sh build`). Use the absolute path to `printpipe.sh` everywhere below; this
README uses `/path/to/3dpal/printpipe.sh`.

### Claude Code

```sh
./printpipe.sh mcp-add
```

That runs:

```sh
claude mcp add print-pipeline -s user -- /path/to/3dpal/printpipe.sh mcp
```

Settings you change from the defaults (for example `PRINTPIPE_PORT=8090 ./printpipe.sh
mcp-add`) are passed to the session with `-e`. `-s user` makes it available in every project; use `-s project` to share it through
`.mcp.json` in a repository, or `-s local` for this folder only. In a session, `/mcp`
lists the server and its ten tools.

### Claude Desktop

Open Settings → Developer → Edit Config, add the server to `claude_desktop_config.json`,
and restart Claude Desktop:

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

The file is at `~/Library/Application Support/Claude/claude_desktop_config.json` on
macOS and `%APPDATA%\Claude\claude_desktop_config.json` on Windows. The tools appear under
the tools icon in a new chat.

### Without the script (Windows, or any MCP client)

`printpipe.sh` needs bash (macOS, Linux, or WSL and Git Bash on Windows). Any MCP client
can run Docker directly instead; create the volume and folder once:

```sh
docker volume create print-data
mkdir -p ~/prints
```

```json
{
  "mcpServers": {
    "print-pipeline": {
      "command": "docker",
      "args": ["run", "-i", "--rm", "--platform", "linux/amd64",
               "-p", "127.0.0.1:8080:8080",
               "-v", "print-data:/data",
               "-v", "/Users/you/prints:/work",
               "print-pipeline:latest"]
    }
  }
}
```

Use your own absolute path for the prints folder (on Windows, `C:\\Users\\you\\prints`).
Without the script, stop any standalone viewer yourself before a session.

### Check it works

1. `./printpipe.sh status` shows the image as built.
2. In a new session, ask: "List the printers." The agent calls `list_printers`.
3. Ask: "Make a 20 mm calibration cube, project name test-cube." The cube appears at
   <http://localhost:8080> and the agent shows preview images.

### Troubleshooting

| Symptom | Fix |
| --- | --- |
| The server shows as failed in `/mcp` or Claude Desktop | Run `./printpipe.sh mcp < /dev/null` in a terminal: it prints the reason (Docker not running, image not built). |
| `/mcp` says CONNECTION_CLOSED, and the server was added as a plain `docker run …` command | Port 8080 is taken (often by `./printpipe.sh up`), so Docker refuses to start the container ("Bind for 127.0.0.1:8080 failed: port is already allocated"). Re-register through the script, which frees the port first: `./printpipe.sh mcp-add local` (or `user`). `claude mcp get print-pipeline` should then show Connected. |
| "image print-pipeline:latest not found" | `./printpipe.sh build` |
| Tools work but the viewer doesn't load | Another program holds port 8080: `PRINTPIPE_PORT=8090 ./printpipe.sh mcp-add` registers the session with that port. |
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
go run ./cmd/printpipe -demo -data ./.data -work ./.work
# viewer on http://localhost:8080; npm run dev in web/ proxies /api to it
```

OpenSCAD must be on the PATH (or set `OPENSCAD`) to compile outside Docker.
