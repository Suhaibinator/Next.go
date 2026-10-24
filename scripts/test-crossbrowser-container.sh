#!/bin/sh
# Qualify the actual Linux binary with Firefox/WebKit without host browser state.
set -eu
cd "$(dirname "$0")/.."
task_root=$(pwd -P)
task_artifacts=$(mktemp -d "${TMPDIR:-/tmp}/nextgo-crossbrowser.XXXXXX")
trap 'rm -rf "$task_artifacts"' EXIT HUP INT TERM
mkdir -p "$task_root/.nextgo-test-results/crossbrowser"
task_results=$(mktemp -d "$task_root/.nextgo-test-results/crossbrowser/container.XXXXXX")
printf 'Browser results: %s\n' "$task_results"
docker build --target artifact --output "type=local,dest=$task_artifacts" .
docker build --target workspace-artifact --output "type=local,dest=$task_artifacts/workspace" .
docker build --target static-browser-test -t nextgo-static-browser-test:local .
docker run --rm --init --network none --shm-size=1g \
  -v "$task_artifacts:/artifact:ro" \
  -v "$task_results:/results" -w /src \
  -e NEXTGO_E2E_WORKSPACE_BINARY=/artifact/workspace/server -e NEXTGO_E2E_SERVER_BINARY=/artifact/server -e NEXTGO_E2E_RESULTS=/results \
  nextgo-static-browser-test:local npm run test:crossbrowser -- "$@"
