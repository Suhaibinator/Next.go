#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
task_root=$(pwd -P)
mkdir -p "$task_root/.nextgo-test-results/partial-shell"
task_results=$(mktemp -d "$task_root/.nextgo-test-results/partial-shell/container.XXXXXX")
printf 'Partial-shell prefetch browser results: %s\n' "$task_results"
docker build --target static-browser-test -t nextgo-partial-shell-browser-test:local .
docker run --rm --init --network none --shm-size=1g \
  -v "$task_results:/results" -e NEXTGO_E2E_RESULTS=/results \
  nextgo-partial-shell-browser-test:local node scripts/test-browser.mjs --config tests/partial-shell.config.mjs \
  --project=partial-firefox --project=partial-webkit --project=partial-webkit-touch
