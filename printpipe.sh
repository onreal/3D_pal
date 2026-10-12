#!/usr/bin/env bash
# printpipe.sh: bring the print pipeline container up and down, and connect it to an agent.
#
#   ./printpipe.sh build            build the image
#   ./printpipe.sh up [--demo]      run the viewer and settings page on their own (no agent)
#   ./printpipe.sh down             stop the viewer (and, with --all, any agent session's container)
#   ./printpipe.sh restart          down, then up
#   ./printpipe.sh status           containers, health, printers
#   ./printpipe.sh logs [-f]        the server log from the data volume
#   ./printpipe.sh open             open the viewer in the browser
#   ./printpipe.sh mcp-add          register with Claude Code and print the Claude Desktop config
#   ./printpipe.sh mcp-remove       unregister from Claude Code
#   ./printpipe.sh mcp              what the MCP client runs: frees the port, then serves MCP on stdio
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
VIEWER=print-pipeline-viewer
LABEL=org.printpipe.role
URL="http://localhost:$PORT"
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

# set_run_args: the shared docker run arguments (ports, volumes, viewer URL, demo mode).
# A plain array, since macOS ships bash 3.2 (no mapfile).
set_run_args() {
  RUN_ARGS=(--rm --platform linux/amd64
    -p "127.0.0.1:$PORT:8080"
    -v "$VOLUME:/data" -v "$WORK:/work"
    -e "PRINTPIPE_VIEWER_URL=$URL")
  if [[ "$DEMO" == 1 ]]; then RUN_ARGS+=(-e PRINTPIPE_DEMO=1); fi
}

viewer_running() { [[ -n "$(docker ps -q --filter "name=^${VIEWER}$")" ]]; }

port_owner() {
  docker ps --filter "publish=$PORT" --format '{{.Names}}' | head -n1
}

wait_healthy() {
  local i
  for i in $(seq 1 60); do
    if curl -fsS "$URL/api/health" >/dev/null 2>&1; then return 0; fi
    sleep 0.5
  done
  return 1
}

cmd_build() {
  need_docker
  say "Building $IMAGE (linux/amd64; the first build downloads Bambu Studio, about 220 MB)…"
  docker build --platform linux/amd64 -t "$IMAGE" --build-arg "VERSION=$(git -C "$HERE" describe --always --dirty 2>/dev/null || echo dev)" "$HERE"
  say "Built $IMAGE."
}

cmd_up() {
  if [[ "${1:-}" == "--demo" ]]; then DEMO=1; fi
  need_docker; need_image; prepare
  if viewer_running; then say "Already up: $URL"; return 0; fi
  local owner; owner=$(port_owner)
  if [[ -n "$owner" ]]; then die "port $PORT is used by container $owner (an agent session?). End that session, or set PRINTPIPE_PORT."; fi
  set_run_args
  # No -i: without an agent on stdin the server runs the viewer and settings page only.
  docker run -d --name "$VIEWER" --label "$LABEL=viewer" "${RUN_ARGS[@]}" "$IMAGE" >/dev/null
  if wait_healthy; then
    say "Up: $URL (settings: $URL/settings, workspace: $WORK)"
  else
    docker logs "$VIEWER" 2>&1 | tail -20 >&2 || true
    die "the viewer did not come up; see ./printpipe.sh logs"
  fi
}

cmd_down() {
  need_docker
  if viewer_running; then
    docker stop "$VIEWER" >/dev/null
    say "Viewer stopped."
  else
    say "Viewer is not running."
  fi
  if [[ "${1:-}" == "--all" ]]; then
    local ids; ids=$(docker ps -q --filter "label=$LABEL=mcp")
    if [[ -n "$ids" ]]; then
      docker stop $ids >/dev/null
      say "Stopped agent session containers: $(echo "$ids" | wc -l | tr -d ' ')."
    fi
  fi
}

cmd_status() {
  need_docker
  local rows; rows=$(docker ps --filter "label=$LABEL" --format '{{.Names}}\t{{.Label "'"$LABEL"'"}}\t{{.Status}}\t{{.Ports}}')
  if [[ -z "$rows" ]]; then
    say "Not running. Start the viewer with ./printpipe.sh up, or start an agent session."
  else
    printf 'NAME\tROLE\tSTATUS\tPORTS\n%s\n' "$rows" | column -t -s $'\t' >&2
  fi
  if curl -fsS "$URL/api/health" >/dev/null 2>&1; then
    say "Viewer: $URL (healthy)"
    curl -fsS "$URL/api/printers?status=0" 2>/dev/null |
      grep -o '"name":"[^"]*","kind":"[^"]*","default":[a-z]*' |
      sed -E 's/"name":"([^"]*)","kind":"([^"]*)","default":(.*)/  printer: \1 (\2)\3/; s/true$/, default/; s/false$//' >&2 || true
  fi
  docker image inspect "$IMAGE" >/dev/null 2>&1 && say "Image: $IMAGE" || say "Image: $IMAGE not built (./printpipe.sh build)"
}

cmd_logs() {
  need_docker
  local follow=""
  if [[ "${1:-}" == "-f" ]]; then follow="-f"; fi
  need_image
  docker run --rm --platform linux/amd64 --entrypoint tail -v "$VOLUME:/data:ro" "$IMAGE" -n 200 $follow /data/logs/printpipe.log
}

cmd_open() {
  if command -v open >/dev/null 2>&1; then open "$URL"
  elif command -v xdg-open >/dev/null 2>&1; then xdg-open "$URL"
  elif command -v cmd.exe >/dev/null 2>&1; then cmd.exe /c start "$URL"
  else say "Open $URL in your browser."; fi
}

# mcp: started by the MCP client for each session. stdout is the protocol, so nothing
# else may print there. The standalone viewer is stopped first so the port is free; the
# session's container serves the viewer itself and is removed when the session ends.
cmd_mcp() {
  need_docker; need_image; prepare
  if viewer_running; then
    docker stop "$VIEWER" >/dev/null 2>&1 || true
    say "printpipe: stopped the standalone viewer; this session serves $URL"
  fi
  set_run_args
  exec docker run -i --label "$LABEL=mcp" "${RUN_ARGS[@]}" "$IMAGE"
}

desktop_config_path() {
  case "$(uname -s)" in
    Darwin) echo "$HOME/Library/Application Support/Claude/claude_desktop_config.json" ;;
    Linux) echo "${XDG_CONFIG_HOME:-$HOME/.config}/Claude/claude_desktop_config.json" ;;
    *) echo "%APPDATA%\\Claude\\claude_desktop_config.json" ;;
  esac
}

# Settings that differ from the defaults, as NAME=value, so the session gets them too.
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
  local envflags=() line
  while IFS= read -r line; do [[ -n "$line" ]] && envflags+=(-e "$line"); done < <(custom_env)
  if command -v claude >/dev/null 2>&1; then
    claude mcp remove print-pipeline -s "$scope" >/dev/null 2>&1 || true
    claude mcp add print-pipeline -s "$scope" ${envflags[@]+"${envflags[@]}"} -- "$SELF" mcp
    say "Registered with Claude Code ($scope scope). Run /mcp in a session to check it."
  else
    say "Claude Code (claude) is not installed; skipping. Its command would be:"
    say "  claude mcp add print-pipeline -s $scope ${envflags[*]:-}-- \"$SELF\" mcp"
  fi
  say ""
  say "Claude Desktop: add this to $(desktop_config_path), then restart Claude Desktop:"
  local env_json="" sep=""
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
