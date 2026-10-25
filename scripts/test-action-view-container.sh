#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
task_root=$(pwd -P)
mkdir -p "$task_root/.nextgo-test-results/action-view"
task_results=$(mktemp -d "$task_root/.nextgo-test-results/action-view/container.XXXXXX")
printf 'Action-view browser results: %s\n' "$task_results"
docker build --target static-browser-test -t nextgo-action-view-browser-test:local .
docker run --rm --init --network none --shm-size=1g \
  -v "$task_results:/results" -e NEXTGO_E2E_RESULTS=/results \
  nextgo-action-view-browser-test:local node scripts/test-browser.mjs --config tests/action-view.config.mjs \
  --project=action-view-firefox --project=action-view-webkit --project=action-view-webkit-touch
