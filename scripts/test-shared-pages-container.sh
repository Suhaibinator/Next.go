#!/bin/sh
# Public snapshot sharing behind one stable Host, with normal/failing replicas.
set -eu
cd "$(dirname "$0")/.."
task_root=$(pwd -P)
task_artifacts=$(mktemp -d "${TMPDIR:-/tmp}/nextgo-shared-pages.XXXXXX")
trap 'rm -rf "$task_artifacts"' EXIT HUP INT TERM
mkdir -p "$task_root/.nextgo-test-results/shared-pages"
task_results=$(mktemp -d "$task_root/.nextgo-test-results/shared-pages/container.XXXXXX")
printf 'Shared-page browser results: %s\n' "$task_results"
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
  --config tests/shared-pages.config.mjs --project=shared-pages-firefox --project=shared-pages-webkit --project=shared-pages-touch
