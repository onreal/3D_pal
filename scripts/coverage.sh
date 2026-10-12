#!/bin/sh
# Fails when any core package's statement coverage is below the threshold (default 80%).
set -eu
threshold=${1:-80}
# Packages with tests; helper packages (storetest, printertest, pipelinetest) are covered through their users.
pkgs=$(go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./internal/...)
out=$(go test -cover $pkgs 2>&1) || { echo "$out"; exit 1; }
echo "$out"
echo "$out" | awk -v t="$threshold" '
  $1 == "ok" && /coverage:/ {
    for (i = 1; i <= NF; i++) if ($i == "coverage:") { pct = $(i + 1); sub("%", "", pct) }
    if (pct + 0 < t) { printf "FAIL coverage %s%% < %s%%: %s\n", pct, t, $2; bad = 1 }
  }
  END { exit bad }'
