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
docker run --rm --network none -v "$task_root:/src:ro" -v "$task_artifacts:/out" -w /src \
  -e GOTOOLCHAIN=local -e GOCACHE=/tmp/nextgo-go-cache golang:1.24-bookworm \
  go build -trimpath -o /out/nextgo-cache ./cmd/nextgo-cache
docker run --rm --init --network none --shm-size=1g \
  -v "$task_root:/src:ro" -v "$task_artifacts:/artifact:ro" -v "$task_results:/results" -w /src \
  -e NEXTGO_E2E_SERVER_BINARY=/artifact/server -e NEXTGO_E2E_CACHE_BINARY=/artifact/nextgo-cache -e NEXTGO_E2E_RESULTS=/results \
  mcr.microsoft.com/playwright:v1.64.1-noble node scripts/test-browser.mjs \
  --config tests/shared-pages.config.mjs --project=shared-pages-firefox --project=shared-pages-webkit --project=shared-pages-touch
