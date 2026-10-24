#!/bin/sh
# The fixture launches the actual executable in native HTTP and Lambda modes.
set -eu
cd "$(dirname "$0")/.."
task_image="nextgo-deployment-test:local"
docker build --target deployment-test -t "$task_image" .
docker run --rm --init --read-only --network none --cap-drop ALL \
  --security-opt no-new-privileges --pids-limit 128 --memory 512m \
  --tmpfs /cache-tests:rw,nosuid,nodev,noexec,size=64m,uid=65532,gid=65532,mode=0700 "$task_image"
