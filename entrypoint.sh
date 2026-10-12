#!/bin/sh
# stdout belongs to the MCP protocol: nothing else may print there.
set -e
mkdir -p /data/logs /data/profiles /work
rm -f /tmp/.X99-lock
Xvfb :99 -screen 0 1280x1024x24 -nolisten tcp >/data/logs/xvfb.log 2>&1 &
export DISPLAY=:99
exec printpipe "$@" 2>>/data/logs/printpipe.log
