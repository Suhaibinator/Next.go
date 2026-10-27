# Lambda ZIP packaging

This optional target prepares a Linux custom-runtime package. It performs no
AWS calls, uploads, provisioning or deployment. Native builds remain unchanged.

```sh
go run ./cmd/nextgo build -target lambda -arch amd64 -out ./dist/lambda ./examples/demo
# For a matching ARM deployment, select -arch arm64 instead.
```

An explicit architecture is required. The CLI runs on the build host; only the
final server link uses Linux, the selected architecture, CGO disabled and the
baseline GOAMD64=v1 or GOARM64=v8.0. The browser still uses Go/WASM and any
prerender capture builds and runs on the host before the final target link.
Pure-Go dependencies must support the selected Linux target; CGO-only dependencies
cause a build error. Omitted Lambda output is dist/lambda; ordinary native output
remains dist/demo/server.

The output has two files:

- `function.zip`, containing exactly one regular executable `bootstrap` at its root.
- `deployment.json`, recording `provided.al2023`, provider architecture,
  exact bootstrap/ZIP sizes and SHA-256 hashes, inspected static ELF information,
  actual Go build version/target provenance, runtime ID, source fingerprint and
  the sorted embedded asset inventory. Asset `embeddedPath` values name physical
  compiler inputs; optional `defaultRequestPath` values describe ordinary decoded public
  (`/assets/`) and runtime request paths only. Runtime base/asset configuration
  may remap those defaults; metadata-file request paths are not guessed.

Previous packages are recognized only after their ZIP bootstrap has been
extracted into a bounded owned temporary file and its actual ELF/Go build
provenance matches the deployment metadata; matching hashes alone are insufficient.

The packager inspects actual ELF bytes: 64-bit little-endian matching machine,
no interpreter or shared-library dependency, and entry inside an executable
load range. It reopens the completed ZIP and hashes its actual bootstrap.
Conservative package limits are 50,000,000 ZIP bytes and 250,000,000 bootstrap
bytes, below the documented Lambda ZIP limits of 50 MB compressed and 250 MB
uncompressed. Metadata is bounded to 1 MiB, the inventory to 4,096 asset files and
250,000,000 aggregate raw asset bytes, and traversal to 16,384 visited entries.
These counters do not claim a process memory or wall-time guarantee.

Output must be outside selected module roots or below an excluded module-root
`dist` directory. It must not overlap source/public/metadata inputs, a declared
asset-tool input or a Go embed pattern that could capture the new package.
The same checks apply to sibling staging, backup reservations and ELF-inspection
temporary files before creation. Unknown nonempty directories are
refused; only an empty directory or a validated previous Lambda package is
replaceable. Complete staging precedes publication. A rename backup permits
rollback on publication failure; this offline publisher has a small rename
interval and is not a zero-downtime deployment mechanism. A post-commit backup
cleanup failure produces a warning naming the cleanup path.

Package validity is separate from request delivery. The existing buffered
adapter caps response bodies at 4 MiB and complete envelopes at 6 MiB.
Metadata explicitly records buffered transport as **UNVERIFIED** and warns about
raw assets above the identity-body limit. Browser gzip may allow the WASM bundle
to fit; packaging does not prove negotiation, middleware, dynamic response or
provider behavior. Identity-only clients, no-transform middleware and large
responses may exceed those bounds. Nothing is silently moved to S3 or a CDN.
The runtime's optional `NEXTGO_LAMBDA_RESPONSE_MODE=streaming` selects its existing
streaming protocol; the deployment integration must separately support it.

Authored artifact tests build real generated applications, validate both target
ELFs/ZIPs and execute the ZIP's exact extracted bootstrap against a local mock
Runtime API when Linux and the test-process architecture match. They check real SSR/navigation/API and
asset gzip/base64 bytes, buffering/stream framing and shutdown. Mock cleanup has
an independent cancellation context, force-closes owned connections and requires
bounded Close/Serve/connection completion. Passing cleanup requires actual child
exit zero and listener refusal. A timed-out cleanup fails; it does not claim
G06 worker-failure ownership or observed outer-host cleanup. Foreign target
inspection does not prove execution, and Go process architecture alone does not
prove hardware origin or distinguish native hardware from emulation. These new tests and this implementation
remain unexecuted until separately qualified; no AWS or production-maturity
claim follows from source inspection. Retention, props and other in-progress
framework features retain their existing independent qualification limits.

Provider background: [custom runtimes](https://docs.aws.amazon.com/lambda/latest/dg/runtimes-provided.html),
[Go custom-runtime packaging](https://github.com/aws/aws-lambda-go/blob/main/README.md)
and [Lambda quotas](https://docs.aws.amazon.com/lambda/latest/dg/gettingstarted-limits.html).
This milestone neither queries the account nor verifies a deployment against
its current service configuration.
