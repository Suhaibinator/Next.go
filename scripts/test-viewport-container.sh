#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
task_root=$(pwd -P)
mkdir -p "$task_root/.nextgo-test-results/viewport"
task_results=$(mktemp -d "$task_root/.nextgo-test-results/viewport/container.XXXXXX")
printf 'Viewport prefetch browser results: %s\n' "$task_results"
docker build --target static-browser-test -t nextgo-viewport-browser-test:local .
docker run --rm --init --network none --shm-size=1g \
  -v "$task_results:/results" -e NEXTGO_E2E_RESULTS=/results \
  nextgo-viewport-browser-test:local node scripts/test-browser.mjs --config tests/viewport.config.mjs \
  --project=viewport-firefox --project=viewport-webkit --project=viewport-webkit-touch
