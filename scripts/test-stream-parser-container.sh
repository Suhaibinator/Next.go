#!/bin/sh
# Exercise real streamed HTML across deliberately separated parser writes.
set -eu
cd "$(dirname "$0")/.."
task_root=$(pwd -P)
task_artifacts=$(mktemp -d "${TMPDIR:-/tmp}/nextgo-stream-parser.XXXXXX")
trap 'rm -rf "$task_artifacts"' EXIT HUP INT TERM
mkdir -p "$task_root/.nextgo-test-results/stream-parser"
task_results=$(mktemp -d "$task_root/.nextgo-test-results/stream-parser/container.XXXXXX")
printf 'Stream-parser browser results: %s\n' "$task_results"
docker build --target artifact --output "type=local,dest=$task_artifacts" .
docker build --target static-browser-test -t nextgo-static-browser-test:local .
docker run --rm --init --network none --shm-size=1g \
  -v "$task_artifacts:/artifact:ro" -v "$task_results:/results" -w /src \
  -e NEXTGO_E2E_SERVER_BINARY=/artifact/server -e NEXTGO_E2E_RESULTS=/results \
  nextgo-static-browser-test:local npm run test:stream-parser -- \
  --project=stream-parser-firefox --project=stream-parser-webkit --project=stream-parser-webkit-touch
