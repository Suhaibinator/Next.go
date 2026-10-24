#!/bin/sh
# Separate native app and cache-service processes, with Linux browser engines.
set -eu
cd "$(dirname "$0")/.."
task_root=$(pwd -P)
task_artifacts=$(mktemp -d "${TMPDIR:-/tmp}/nextgo-shared-browser.XXXXXX")
trap 'rm -rf "$task_artifacts"' EXIT HUP INT TERM
mkdir -p "$task_root/.nextgo-test-results/shared-cache"
task_results=$(mktemp -d "$task_root/.nextgo-test-results/shared-cache/container.XXXXXX")
printf 'Shared-cache browser results: %s\n' "$task_results"
docker build --target artifact --output "type=local,dest=$task_artifacts" .
# Export the cache through its Docker build stage: a bind-mounted checkout can
# have a different UID and fail Go's automatic VCS metadata inspection.
docker build --target cache-artifact --output "type=local,dest=$task_artifacts/cache" .
# Own the pinned test dependencies inside the image, independent of host npm.
docker build --target static-browser-test -t nextgo-static-browser-test:local .
docker run --rm --init --network none --shm-size=1g \
  -v "$task_artifacts:/artifact:ro" -v "$task_results:/results" -w /src \
  -e NEXTGO_E2E_SERVER_BINARY=/artifact/server -e NEXTGO_E2E_CACHE_BINARY=/artifact/cache/nextgo-cache -e NEXTGO_E2E_RESULTS=/results \
  nextgo-static-browser-test:local node scripts/test-browser.mjs \
  --config tests/shared-cache.config.mjs --project=shared-firefox --project=shared-webkit --project=shared-touch
