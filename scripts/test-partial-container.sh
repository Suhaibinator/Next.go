#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
task_root=$(pwd -P)
mkdir -p "$task_root/.nextgo-test-results/partial"
task_results=$(mktemp -d "$task_root/.nextgo-test-results/partial/container.XXXXXX")
printf 'Partial prerender browser results: %s\n' "$task_results"
docker build --target static-browser-test -t nextgo-partial-browser-test:local .
# The fixture performs its own build with the region provider disabled, then
# runs three standalone replicas with the static providers disabled instead.
docker run --rm --init --network none --shm-size=1g \
  -v "$task_results:/results" -e NEXTGO_E2E_RESULTS=/results \
  nextgo-partial-browser-test:local node scripts/test-browser.mjs --config tests/partial.config.mjs \
  --project=partial-firefox --project=partial-webkit --project=partial-webkit-touch
