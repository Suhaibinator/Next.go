#!/bin/sh
# Validate the generated app against the pinned official OTLP collector.
set -eu
cd "$(dirname "$0")/.."
docker build --target collector-test -t nextgo-collector-test:local .
docker run --rm --init --read-only --network none --cap-drop ALL \
  --security-opt no-new-privileges --pids-limit 128 --memory 512m \
  --tmpfs /tmp:rw,noexec,nosuid,size=64m,mode=1777 nextgo-collector-test:local
