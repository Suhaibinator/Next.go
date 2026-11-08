# Local AWS Runtime Interface Emulator qualification

The generated standalone executable runs under AWS's official Runtime Interface
Emulator (RIE) in both buffered and response-streaming modes. This is local
Runtime API conformance, not hosted AWS qualification. The emulator does not
reproduce Lambda orchestration, IAM, authentication, networking, scaling or
platform limits. See the [upstream scope](https://github.com/aws/aws-lambda-runtime-interface-emulator/tree/d71cae7ddcf3b96ab5ba620fdda2ae7f225438fb).

## Reproduce

Use Linux and a supported Go SDK. The pinned emulator requires Go 1.26.6 or
newer; build it separately when testing the application's Go 1.24 compatibility
target. Ordinary Go checksum verification authenticates its source and
dependencies. No cloud credentials are needed.

```sh
mkdir -p dist/aws-rie
CGO_ENABLED=0 GOBIN="$PWD/dist/aws-rie" go install github.com/aws/aws-lambda-runtime-interface-emulator/cmd/aws-lambda-rie@v0.0.0-20261030183630-d71cae7ddcf3
CGO_ENABLED=0 go run ./cmd/nextgo build -out ./dist/aws-rie/demo ./examples/demo
CGO_ENABLED=0 go test -c -tags=deployment -o ./dist/aws-rie/deployment.test ./tests/deployment
NEXTGO_RIE_BINARY="$PWD/dist/aws-rie/aws-lambda-rie" \
NEXTGO_RIE_SHA256="$(sha256sum dist/aws-rie/aws-lambda-rie | cut -d ' ' -f1)" \
NEXTGO_TEST_BINARY="$PWD/dist/aws-rie/demo/server" \
NEXTGO_TEST_BINARY_SHA256="$(sha256sum dist/aws-rie/demo/server | cut -d ' ' -f1)" \
./dist/aws-rie/deployment.test '-test.run=^TestAWSRIE' -test.v -test.timeout=120s
```

The `aws-rie` CI job also runs the same harness in a scratch container as UID
65532 with a read-only root, no network, dropped capabilities, no new privileges,
128 PIDs, 256 MiB memory and a 32 MiB non-executable temporary filesystem.
It requires all 32 cases without skips. CI configuration is checked locally;
remote execution remains unverified for this working tree.

## Checked contract

`tests/deployment/aws_rie_test.go` verifies executable SHA-256 pins before
starting each fixture. Every emulator/runtime pair receives a complete synthetic
environment, an empty working directory and separate loopback listeners. It
inherits no cloud identity, application files or proxy settings.

The root and mounted French/trailing-slash configurations each run through
buffered and streaming modes. Seven cases per configuration cover cold/warm
health requests, SSR, navigation envelopes, HEAD, gzip/base64 WASM delivery,
generated mutations with subsequent SSR, and unsupported-event error reporting
followed by recovery. Four additional session cases reuse the existing signed
cookie/authentication/native-form/action-and-view contract. Those cases each
start three independent runtime fixtures, for 16 emulator lifecycles per matrix.
Streaming checks decode the HTTP integration metadata and eight-NUL delimiter;
buffered checks decode the response envelope and binary payload.

The harness verifies ownership by parent PID and executable, waits for the
emulator to exit, verifies its observed runtime children are absent, and checks
both listeners close. RIE itself kills the runtime during shutdown. This proves
local cleanup only; it provides no graceful application-drain evidence.

## November 7 evidence and limits

Go 1.27.3 and compatibility-only Go 1.24.13 each pass 32 host and 32 restricted
container cases. Both also pass the 32 host cases under the Go race detector and
deployment-package vet. The emulator is built with Go 1.27.3 from checksum-verified
official v1.38 source. The application binaries are the pinned compiled-routing
artifacts; a full Go-source comparison verifies the sole subsequent Go change
is this deployment test. No application runtime change was needed.

[Machine-readable provenance](benchmarks/aws-rie-qualification.json) retains
module sums, executable/source/log hashes, case counts and failed fixture
attempts. A downloaded official release executable was used for exploratory
probes only. Its hash is observed locally; no publisher checksum asset was
available. Qualification uses the separately source-built executable.

RIE's public invocation endpoint buffers the response, so this matrix does not
prove early flush at a hosted integration. Emulator REPORT memory fields are
configured values, not measured RSS or performance evidence. Real AWS Function
URL/API Gateway behavior, IAM, timeouts, payload limits, cancellation,
concurrency, cold starts, cleanup and cost remain open deployment work. Existing
mock early-flush/deadline and native graceful-drain tests remain separate
evidence. Go 1.24's successful conformance results do not waive its known
[standard-library advisories](SECURITY_QUALIFICATION.md).
