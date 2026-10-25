#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
task_root=$(pwd -P)
mkdir -p "$task_root/.nextgo-test-results/generated-actions"
task_results=$(mktemp -d "$task_root/.nextgo-test-results/generated-actions/container.XXXXXX")
printf 'Generated action browser results: %s\n' "$task_results"
docker build --target static-browser-test -t nextgo-generated-actions-browser-test:local .
docker run --rm --init --network none --shm-size=1g \
  -v "$task_results:/results" -e NEXTGO_E2E_RESULTS=/results \
  nextgo-generated-actions-browser-test:local node scripts/test-browser.mjs --config tests/generated-actions.config.mjs \
  --project=generated-actions-firefox --project=generated-actions-webkit --project=generated-actions-webkit-touch
