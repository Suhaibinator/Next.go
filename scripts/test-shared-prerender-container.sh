#!/bin/sh
# The authority, real Go build and browser all share one offline Linux container.
# /src is the disposable writable image copy; runtime apps run from owned /tmp.
set -eu
cd "$(dirname "$0")/.."
task_root=$(pwd -P)
mkdir -p "$task_root/.nextgo-test-results/shared-prerender"
task_results=$(mktemp -d "$task_root/.nextgo-test-results/shared-prerender/container.XXXXXX")
task_image="nextgo-shared-prerender-browser-test:local"
printf 'Shared-prerender browser results: %s\n' "$task_results"
docker build --target static-browser-test -t "$task_image" .
docker run --rm --init --network none --shm-size=1g \
  -v "$task_results:/results" -e NEXTGO_E2E_RESULTS=/results \
  "$task_image" node scripts/test-browser.mjs --config tests/shared-prerender.config.mjs \
  --project=shared-prerender-firefox --project=shared-prerender-webkit --project=shared-prerender-touch
