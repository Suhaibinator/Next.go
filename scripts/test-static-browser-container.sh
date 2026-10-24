#!/bin/sh
# Build/export with Go 1.24 and serve only files to Firefox/WebKit/touch.
set -eu
cd "$(dirname "$0")/.."
task_root=$(pwd -P)
task_results="$task_root/.nextgo-test-results/crossbrowser/static-container"
mkdir -p "$task_results"
docker build --target static-browser-test -t nextgo-static-browser-test:local .
docker run --rm --init --network none --shm-size=1g \
  -v "$task_results:/results" -e NEXTGO_E2E_RESULTS=/results \
  nextgo-static-browser-test:local
