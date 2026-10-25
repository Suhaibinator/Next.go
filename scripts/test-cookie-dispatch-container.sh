#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
task_root=$(pwd -P)
mkdir -p "$task_root/.nextgo-test-results/cookie-dispatch"
task_results=$(mktemp -d "$task_root/.nextgo-test-results/cookie-dispatch/container.XXXXXX")
printf 'Cookie and typed-dispatch browser results: %s\n' "$task_results"
docker build --target static-browser-test -t nextgo-cookie-dispatch-browser-test:local .
docker run --rm --init --network none --shm-size=1g \
  -v "$task_results:/results" -e NEXTGO_E2E_RESULTS=/results \
  nextgo-cookie-dispatch-browser-test:local node scripts/test-browser.mjs --config tests/cookie-dispatch.config.mjs \
  --project=cookie-dispatch-firefox --project=cookie-dispatch-webkit --project=cookie-dispatch-webkit-touch
