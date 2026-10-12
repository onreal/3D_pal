#!/usr/bin/env bash
# printpipe.sh: run the print pipeline server and connect agents to it.
#
# One long-running container serves the viewer (http://localhost:8080) and MCP over
# HTTP (http://localhost:8080/mcp). Any number of agent sessions connect to it at once;
# the viewer lists them and shows which client holds each one.
#
#   ./printpipe.sh build            build the image
#   ./printpipe.sh up [--demo]      start the server (restarts with Docker; recreated after a build)
#   ./printpipe.sh down             stop and remove the server
#   ./printpipe.sh restart          down, then up
#   ./printpipe.sh status           server health, open agent sessions, printers
#   ./printpipe.sh logs [-f]        the server log from the data volume
#   ./printpipe.sh open             open the viewer in the browser
#   ./printpipe.sh mcp-add [scope]  register with Claude Code over HTTP; print the Claude Desktop config
#   ./printpipe.sh mcp-remove       unregister from Claude Code
#   ./printpipe.sh mcp              for stdio-only clients: starts the server if needed and bridges stdio to /mcp
#
# Settings come from the environment (defaults in brackets):
#   PRINTPIPE_IMAGE  [print-pipeline:latest]   PRINTPIPE_PORT  [8080]
#   PRINTPIPE_WORK   [$HOME/prints]            PRINTPIPE_VOLUME [print-data]
#   PRINTPIPE_DEMO   [0]  1 adds a simulated printer when none is configured
set -euo pipefail

# MCP clients (Claude Desktop in particular) start servers with a minimal PATH; add the
# places Docker installs its CLI so `docker` is found.
export PATH="$PATH:/usr/local/bin:/opt/homebrew/bin:/Applications/Docker.app/Contents/Resources/bin:$HOME/.docker/bin"

IMAGE=${PRINTPIPE_IMAGE:-print-pipeline:latest}
PORT=${PRINTPIPE_PORT:-8080}
WORK=${PRINTPIPE_WORK:-$HOME/prints}
VOLUME=${PRINTPIPE_VOLUME:-print-data}
DEMO=${PRINTPIPE_DEMO:-0}
SERVER=${PRINTPIPE_CONTAINER:-print-pipeline}
LEGACY=print-pipeline-viewer
LABEL=org.printpipe.role
URL="http://localhost:$PORT"
MCP_URL="http://127.0.0.1:$PORT/mcp"
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
SELF="$HERE/$(basename "${BASH_SOURCE[0]}")"

# Everything but MCP traffic goes to stderr: in `mcp` mode stdout belongs to the protocol.
say() { printf '%s\n' "$*" >&2; }
die() { say "printpipe: $*"; exit 1; }

need_docker() {
  command -v docker >/dev/null 2>&1 || die "Docker is not installed. Install Docker Desktop (macOS, Windows) or Docker Engine (Linux)."
  docker info >/dev/null 2>&1 || die "Docker is not running. Start Docker Desktop and try again."
}

need_image() {
  docker image inspect "$IMAGE" >/dev/null 2>&1 || die "image $IMAGE not found: run ./printpipe.sh build first"
}

prepare() {
  mkdir -p "$WORK"
  docker volume inspect "$VOLUME" >/dev/null 2>&1 || docker volume create "$VOLUME" >/dev/null
}

server_running() { [[ "$(docker container inspect -f '{{.State.Running}}' "$SERVER" 2>/dev/null)" == "true" ]]; }
server_exists() { docker container inspect "$SERVER" >/dev/null 2>&1; }

healthy() { curl -fsS "$URL/api/health" >/dev/null 2>&1; }

wait_healthy() {
  local i
  for i in $(seq 1 60); do
    if healthy; then return 0; fi
    sleep 0.5
  done
  return 1
}

# Containers from the earlier one-container-per-session setup.
remove_legacy() {
  docker rm -f "$LEGACY" >/dev/null 2>&1 || true
  local old; old=$(docker ps -aq --filter "label=$LABEL=mcp")
  if [[ -n "$old" ]]; then docker rm -f $old >/dev/null 2>&1 || true; fi
}

cmd_build() {
  need_docker
  say "Building $IMAGE (linux/amd64; the first build downloads Bambu Studio, about 220 MB)…"
  docker build --platform linux/amd64 -t "$IMAGE" --build-arg "VERSION=$(git -C "$HERE" describe --always --dirty 2>/dev/null || echo dev)" "$HERE"
  say "Built $IMAGE. Run ./printpipe.sh up (it recreates a running server on the new image)."
}

cmd_up() {
  if [[ "${1:-}" == "--demo" ]]; then DEMO=1; fi
  need_docker; need_image; prepare; remove_legacy
  if server_exists; then
    local want have
    want=$(docker image inspect -f '{{.Id}}' "$IMAGE")
    have=$(docker container inspect -f '{{.Image}}' "$SERVER")
    if [[ "$want" != "$have" ]]; then
      say "The image changed; recreating the server…"
      docker rm -f "$SERVER" >/dev/null
    elif server_running; then
      if healthy; then say "Up: $URL (MCP: $MCP_URL)"; return 0; fi
    else
      docker start "$SERVER" >/dev/null
    fi
  fi
  if ! server_exists; then
    local owner; owner=$(docker ps --filter "publish=$PORT" --format '{{.Names}}' | head -n1)
    if [[ -n "$owner" ]]; then die "port $PORT is used by container $owner; stop it or set PRINTPIPE_PORT"; fi
    local args=(-d --name "$SERVER" --label "$LABEL=server" --restart unless-stopped --platform linux/amd64
      -p "127.0.0.1:$PORT:8080" -v "$VOLUME:/data" -v "$WORK:/work" -e "PRINTPIPE_VIEWER_URL=$URL")
    if [[ "$DEMO" == 1 ]]; then args+=(-e PRINTPIPE_DEMO=1); fi
    docker run "${args[@]}" "$IMAGE" >/dev/null
  fi
  if wait_healthy; then
    say "Up: $URL (MCP: $MCP_URL, workspace: $WORK)"
  else
    docker logs "$SERVER" 2>&1 | tail -20 >&2 || true
    die "the server did not come up; see ./printpipe.sh logs"
  fi
}

cmd_down() {
  need_docker
  if server_exists; then
    docker rm -f "$SERVER" >/dev/null
    say "Server stopped. Agent sessions are disconnected until ./printpipe.sh up."
  else
    say "Server is not running."
  fi
  remove_legacy
}

cmd_status() {
  need_docker
  if server_running; then
    say "Server: $(docker container inspect -f '{{.State.Status}} since {{.State.StartedAt}}' "$SERVER")"
  else
    say "Server: not running (./printpipe.sh up)"
  fi
  if healthy; then
    say "Viewer: $URL    MCP: $MCP_URL"
    local sessions
    sessions=$(curl -fsS "$URL/api/sessions" 2>/dev/null | tr '{' '\n' | grep '"open":true' || true)
    if [[ -z "$sessions" ]]; then
      say "Sessions: none open"
    else
      say "Sessions:"
      printf '%s\n' "$sessions" | while IFS= read -r s; do
        local client transport calls project
        client=$(printf '%s' "$s" | sed -nE 's/.*"client":"([^"]*)".*/\1/p')
        transport=$(printf '%s' "$s" | sed -nE 's/.*"transport":"([^"]*)".*/\1/p')
        calls=$(printf '%s' "$s" | sed -nE 's/.*"calls":([0-9]+).*/\1/p')
        project=$(printf '%s' "$s" | sed -nE 's/.*"project":"([^"]*)".*/\1/p')
        say "  $client via $transport, $calls calls${project:+, project $project}"
      done
    fi
    curl -fsS "$URL/api/printers?status=0" 2>/dev/null |
      grep -o '"name":"[^"]*","kind":"[^"]*","default":[a-z]*' |
      sed -E 's/"name":"([^"]*)","kind":"([^"]*)","default":(.*)/Printer: \1 (\2)\3/; s/true$/, default/; s/false$//' >&2 || true
  fi
  if docker image inspect "$IMAGE" >/dev/null 2>&1; then say "Image: $IMAGE"; else say "Image: $IMAGE not built (./printpipe.sh build)"; fi
}

cmd_logs() {
  need_docker; need_image
  local follow=""
  if [[ "${1:-}" == "-f" ]]; then follow="-f"; fi
  docker run --rm --platform linux/amd64 --entrypoint tail -v "$VOLUME:/data:ro" "$IMAGE" -n 200 $follow /data/logs/printpipe.log
}

cmd_open() {
  if command -v open >/dev/null 2>&1; then open "$URL"
  elif command -v xdg-open >/dev/null 2>&1; then xdg-open "$URL"
  elif command -v cmd.exe >/dev/null 2>&1; then cmd.exe /c start "$URL"
  else say "Open $URL in your browser."; fi
}

# mcp: for MCP clients that only speak stdio (Claude Desktop). stdout is the protocol, so
# nothing else may print there. The bridge runs inside the server container and relays to
# /mcp, so this session joins the same server as every other one. A client may stop this
# script with a signal instead of closing stdin; the bridge is then stopped explicitly.
cmd_mcp() {
  need_docker; need_image
  if ! server_running || ! healthy; then cmd_up; fi
  local pidfile="/tmp/printpipe-bridge-$$.pid"
  # shellcheck disable=SC2064
  trap "docker exec $SERVER sh -c 'kill \$(cat $pidfile) 2>/dev/null; rm -f $pidfile' >/dev/null 2>&1 || true" EXIT
  trap 'exit 143' TERM INT HUP
  docker exec -i "$SERVER" printpipe bridge --pidfile "$pidfile" <&0 &
  wait $!
}

desktop_config_path() {
  case "$(uname -s)" in
    Darwin) echo "$HOME/Library/Application Support/Claude/claude_desktop_config.json" ;;
    Linux) echo "${XDG_CONFIG_HOME:-$HOME/.config}/Claude/claude_desktop_config.json" ;;
    *) echo "%APPDATA%\\Claude\\claude_desktop_config.json" ;;
  esac
}

# Settings that differ from the defaults, as NAME=value, for the stdio bridge config.
custom_env() {
  [[ "$IMAGE" != "print-pipeline:latest" ]] && echo "PRINTPIPE_IMAGE=$IMAGE"
  [[ "$PORT" != "8080" ]] && echo "PRINTPIPE_PORT=$PORT"
  [[ "$WORK" != "$HOME/prints" ]] && echo "PRINTPIPE_WORK=$WORK"
  [[ "$VOLUME" != "print-data" ]] && echo "PRINTPIPE_VOLUME=$VOLUME"
  [[ "$DEMO" == "1" ]] && echo "PRINTPIPE_DEMO=1"
  return 0
}

cmd_mcp_add() {
  local scope=${1:-user}
  need_docker; need_image
  cmd_up
  if command -v claude >/dev/null 2>&1; then
    claude mcp remove print-pipeline -s "$scope" >/dev/null 2>&1 || true
    claude mcp add --transport http print-pipeline -s "$scope" "$MCP_URL"
    say "Registered with Claude Code over HTTP ($scope scope): $MCP_URL"
    say "Run /mcp in a session to check it; the viewer at $URL lists every connected session."
  else
    say "Claude Code (claude) is not installed; skipping. Its command would be:"
    say "  claude mcp add --transport http print-pipeline -s $scope $MCP_URL"
  fi
  say ""
  say "Claude Desktop (stdio, bridged into the same server): add this to $(desktop_config_path), then restart it:"
  local env_json="" sep="" line
  while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    env_json+="$sep        \"${line%%=*}\": \"${line#*=}\""
    sep=$',\n'
  done < <(custom_env)
  if [[ -n "$env_json" ]]; then
    printf '{\n  "mcpServers": {\n    "print-pipeline": {\n      "command": "%s",\n      "args": ["mcp"],\n      "env": {\n%s\n      }\n    }\n  }\n}\n' "$SELF" "$env_json"
  else
    printf '{\n  "mcpServers": {\n    "print-pipeline": {\n      "command": "%s",\n      "args": ["mcp"]\n    }\n  }\n}\n' "$SELF"
  fi
}

cmd_mcp_remove() {
  command -v claude >/dev/null 2>&1 || die "Claude Code (claude) is not installed"
  claude mcp remove print-pipeline -s "${1:-user}"
}

usage() { awk 'NR > 1 && /^#/ { sub(/^# ?/, ""); print; next } NR > 1 { exit }' "$SELF" >&2; }

case "${1:-}" in
  build) shift; cmd_build "$@" ;;
  up) shift; cmd_up "$@" ;;
  down) shift; cmd_down "$@" ;;
  restart) shift; cmd_down; cmd_up "$@" ;;
  status) shift; cmd_status "$@" ;;
  logs) shift; cmd_logs "$@" ;;
  open) shift; cmd_open "$@" ;;
  mcp) shift; cmd_mcp "$@" ;;
  mcp-add) shift; cmd_mcp_add "$@" ;;
  mcp-remove) shift; cmd_mcp_remove "$@" ;;
  -h|--help|help|"") usage ;;
  *) usage; exit 2 ;;
esac
