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
docker run --rm --network none -v "$task_root:/src:ro" -v "$task_artifacts:/out" -w /src \
  -e GOTOOLCHAIN=local -e GOCACHE=/tmp/nextgo-go-cache golang:1.24-bookworm \
  go build -trimpath -o /out/nextgo-cache ./cmd/nextgo-cache
docker run --rm --init --network none --shm-size=1g \
  -v "$task_root:/src:ro" -v "$task_artifacts:/artifact:ro" -v "$task_results:/results" -w /src \
  -e NEXTGO_E2E_SERVER_BINARY=/artifact/server -e NEXTGO_E2E_CACHE_BINARY=/artifact/nextgo-cache -e NEXTGO_E2E_RESULTS=/results \
  mcr.microsoft.com/playwright:v1.64.1-noble node scripts/test-browser.mjs \
  --config tests/shared-cache.config.mjs --project=shared-firefox --project=shared-webkit --project=shared-touch
