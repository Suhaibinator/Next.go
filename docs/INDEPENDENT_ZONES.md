# Independent zone releases and fixed ingress

`ZoneRelease` packages one application behind a fixed public identity;
`ZoneIngress` routes a complete, immutable inventory of those endpoints.
Applications can use separate Go modules and selected supported toolchains.
This is an experimental source increment with new qualification pending. It
does not establish full multi-zone parity, a production release, or real AWS
operation. The earlier [aggregate zones](ZONES.md) API remains available and
continues to build one shared native process.

## Release and topology identity

The version-1 `zones.json` source topology declares zone ID, exact public host,
literal base path, optional native tenant metadata, and application directory.
`zones release` selects one exact ID. Its generated native entry imports that
application and the framework, rather than importing peer application packages.
Peer declarations supply routing ownership and document boundaries. The new
qualification must separately establish complete build/fingerprint independence;
source declarations alone are not that evidence.

The topology digest includes normalized ID/host/mount and native tenant
metadata. It excludes application directories, release versions and SDKs so
unchanged ownership can admit independent releases. Tenant metadata is absent
from public release/ingress manifests. The digest is opaque comparison metadata,
not a signature or an authentication claim.

Each `zone-release.json` contains a `release` object and `binarySHA256`. The
release object fixes:

- Version, public zone binding, immutable `releaseID` and 20-hex `runtimeID`.
- Canonical `publicOrigin`, release-qualified `assetPrefix`, and public asset-tree
  SHA-256.
- Topology SHA-256, required nested-zone `documentPaths`, navigation protocol 3
  and action protocol 1.

Use a new release ID for every different artifact. Omission allocates a random
128-bit ID; explicit names such as A1/A2 are operator labels, not published
framework versions. Never reuse an old ID for different binary/assets/configuration.
The CLI refuses an existing output directory and publishes the verified binary
and contract together by staging then renaming a new directory. This is local
artifact publication, not an upload, registry, signature, SBOM or remote binary
attestation. Keep the descriptor, binary checksum and compiler/build provenance
together when deploying. Immutability is a deployment rule; do not edit a
published directory in place or treat ordinary file permissions as tamper protection.

## Building and starting endpoints

The [independent example](../examples/independent-zones/README.md) uses separate
Alpha and Blog modules. From the framework checkout, the initial build flow is:

```sh
go run ./cmd/nextgo zones release -zone alpha \
  -public-origin http://127.0.0.1:4260 -release A1 \
  -out /tmp/independent-alpha-A1 examples/independent-zones/zones.json

go run ./cmd/nextgo zones release -zone blog \
  -public-origin http://127.0.0.1:4260 -release B1 \
  -out /tmp/independent-blog-B1 examples/independent-zones/zones.json
```

Flags precede the topology path. The selected application's module, workspace,
SDK, build flags and dependencies govern its compilation. The aggregate build's
same-module/toolchain restriction does not define this release API. Different
SDK artifacts still require separate qualification and compatible wire contracts;
there is no promise that arbitrary framework/protocol versions can be mixed.

Start each generated executable in a separate terminal on its operator-selected
backend authority:

```sh
/tmp/independent-alpha-A1/server -addr 127.0.0.1:4261 \
  -backend-hosts 127.0.0.1:4261
/tmp/independent-blog-B1/server -addr 127.0.0.1:4262 \
  -backend-hosts 127.0.0.1:4262
```

These are separate long-running processes. `-backend-hosts` accepts a required
comma-separated exact authority allowlist; otherwise the executable reads
`NEXTGO_ZONE_BACKEND_HOSTS`. There are 1–32 entries and a 4 KiB total budget.
An explicit empty flag fails rather than falling back to the environment.
`-addr` selects the listener only. Explicit `-env-dir`/`NEXTGO_ENV_DIR` and
`-env-mode` load that application's environment; peer dotenv directories are not
implicitly loaded into the process.

The native API is `NewZoneRelease(ctx, ZoneReleaseConfig{Zone, Release,
BackendHosts})`. `Zone` supplies normal configuration and an optional hook;
`Release` supplies the validated `ZoneReleaseSpec`. Trusted `Config.Zone()` is
installed before the hook; application request/startup contexts carry the fixed
`PublicOrigin(ctx)`. The hook cannot change
the fixed zone descriptor, public origin, mount, asset prefix or runtime ID, or
remove a required peer document boundary. `Release()` returns an owned snapshot.
`ServeHTTP`, `Close` and `FlushTelemetry` retain the normal app lifecycle.
Lifecycle hooks receive the owning zone/public-origin context while preserving
the caller's cancellation/deadline. Use a non-nil bounded startup context;
canceling it after successful construction is not a serving-lifetime switch.

## Composing ingress

An ingress manifest is a complete version-1 snapshot. Each zone has its public
`binding`, one `activeRelease`, and a `releases` list. Each retained entry contains
the actual parsed release artifact plus an operator-selected backend `origin`.
Origins are HTTP(S) scheme/authority only: no credentials, path prefix, query or
fragment. They are trusted deployment inputs; request data never chooses them.

For example, generate a complete local manifest using the actual descriptors:

```sh
python3 - <<'PY'
import json
from pathlib import Path

a = json.loads(Path('/tmp/independent-alpha-A1/zone-release.json').read_text())
b = json.loads(Path('/tmp/independent-blog-B1/zone-release.json').read_text())
assert a['release']['topologySHA256'] == b['release']['topologySHA256']

def zone(artifact, origin):
    release = artifact['release']
    return {'binding': release['zone'], 'activeRelease': release['releaseID'],
            'releases': [{'artifact': artifact, 'origin': origin}]}

manifest = {'version': 1, 'topologySHA256': a['release']['topologySHA256'],
            'zones': [zone(a, 'http://127.0.0.1:4261'),
                      zone(b, 'http://127.0.0.1:4262')]}
Path('/tmp/independent-ingress-a1-b1.json').write_text(json.dumps(manifest, indent=2))
PY

go run ./cmd/nextgo zones ingress \
  -manifest /tmp/independent-ingress-a1-b1.json -addr 127.0.0.1:4260
```

The CLI validates strict names, required fields, duplicates, versions and bounds
before serving. The limit is 1 MiB, 64 zones and eight retained releases per zone,
including the active release. IDs cannot collide by case. Every artifact must
match its public binding/topology, supply required nested document boundaries,
and name the supported navigation/action protocols. A host cannot mix public
origin schemes across its active/retained releases.

To produce a standalone ingress artifact:

```sh
go run ./cmd/nextgo zones ingress-build \
  -app examples/independent-zones/apps/alpha \
  -out /tmp/independent-ingress-a1-b1 /tmp/independent-ingress-a1-b1.json
/tmp/independent-ingress-a1-b1/server -addr 127.0.0.1:4260
```

The explicit `-app` is a module/toolchain/framework dependency anchor. Its app
configuration is not imported or executed by ingress. The output embeds the
normalized routing snapshot; it does not watch or reload the source JSON file.
The native equivalent is `NewZoneIngress(ctx, ZoneIngressManifest,
ZoneIngressOptions)`. Construction owns the normalized manifest. Route changes
require a new ingress instance/artifact and an operator-controlled cutover.

## Assets, retention and rollback

For Alpha A1 at the public root, helper-generated URLs have these shapes:

```text
/_nextgo/releases/A1/assets/style.css
/_nextgo/releases/A1/_nextgo/<runtimeID>/boot.js
/_nextgo/releases/A1/_nextgo/<runtimeID>/module-<digest>.wasm
```

Blog prefixes the same shapes with `/blog`. The repeated `_nextgo` is deliberate:
the release prefix surrounds that release's own runtime tree. The public tree
admits only `public/**` and `_nextgo/<runtimeID>/**`; private/source trees,
other runtimes, symlinks and special files are rejected. Startup snapshots owned
read-only bytes and checks the canonical asset-tree digest against the artifact.
The bounds are 128 MiB total file bytes, 32 MiB per file, 16,384 entries, 4 KiB
paths and depth 64. Fixed metadata/read cursors prevent callers from mutating
the published snapshot. These are payload/metadata limits, not an RSS guarantee;
an arbitrary blocking native filesystem cannot be preempted during construction.

Only the active release receives pages, APIs and actions. A retained inactive
release receives GET/HEAD only at its exact release-qualified asset paths.
Unversioned `/assets` and runtime URLs do not alias the active release. A missing
retained release/file produces an error/404, never bytes from a newer release.
Directories are not published as listings. Keep each release's paired shim,
core, lazy modules, CSS and public resources together.
This asset-only retirement policy belongs to ingress. A release endpoint does
not learn that an operator marked it inactive and does not disable its own
application handlers. Keep direct backend access protected; compatibility
headers do not make an exposed retired endpoint an asset-only service.

To stage A2, build a new Alpha artifact and start it at a new trusted backend.
Construct a new ingress snapshot with A2 active, retaining A1 and leaving B1's
descriptor/origin unchanged. After cutover, old A1 documents still load their
exact A1 assets, while ordinary Alpha requests select A2. Rollback constructs
another snapshot selecting A1; retain A2 too for documents that have already
observed it. The framework does not supply automatic expiry/garbage collection,
health-based promotion, a load balancer or a zero-downtime cutover controller.

Rollback changes routing, not database schemas, external side effects, sessions,
native process state or mutations already committed by A2. Preserve compatible
action contracts and supply application-level idempotency/reconciliation where
required. A transport failure can mean an unknown outcome; do not blindly retry
an unsafe request. Topology, public host/mount/tenant and origin-scheme changes
can require coordinated new artifacts: one ingress inventory cannot mix old
topology anchors or incompatible public origins. This is not seamless rollback
across arbitrary deployment-policy changes.

Versioned assets pin code bytes, not mutation destinations. Ordinary
`mutation.Call` sends the action protocol and contract revision, not a runtime
build or release pin. The sample's old A1 caller using `preview/v1` can therefore
mutate active A2's compatible v1 handler and receive A2's typed result. Being an
old caller does not itself cause a 409. An unsupported contract revision must be
rejected before its handler runs. Private ingress expectations describe the
selected backend deployment; they do not provide browser-release affinity.
Deploy backward-compatible action handlers or deliberately version contracts;
do not rely on asset retention to fence every old client's mutation.

## Request, origin and security boundaries

Ingress selects exact canonical public Host plus longest complete literal mount
before contacting a backend. A selected zone's 404 never falls through to a
parent. Nested-zone document boundaries force ordinary full-document links;
they do not transfer one app's Go runtime or authorization to another.

The backend authority and public origin are separate. A release checks its exact
backend Host allowlist, then establishes its fixed public origin for URL/origin
policy. `PublicOrigin(ctx)` reports that trusted declaration. It does not prove
actual TLS or a client identity; the incoming `Request.TLS` and `RemoteAddr` are
not rewritten into a fabricated public connection. Framework action origin
checks use the declaration. Authenticate users and authorize each protected
page, API and action independently.
Different paths on one host share browser-origin authority. External databases,
caches, sessions and namespace-wide invalidation need their own tenant/release
policy. Native process globals are separate only when endpoints actually run in
separate processes; constructing several release objects is not a native-code sandbox.

The four `X-Nextgo-Zone-*` expectation headers bind protocol, zone, release and
topology between ingress and endpoint. Ingress strips incoming private fields,
sets exact expected values, requires matching response identity before committing
public headers/body, and strips private fields again. Missing/mismatched identity
fails closed. These headers are not secrets, signatures, ingress authentication,
tenant identity or proof of the remotely running binary's checksum. Protect
backend access through operator network/TLS/platform policy.
The exact names are `X-Nextgo-Zone-Protocol`, `X-Nextgo-Zone-ID`,
`X-Nextgo-Zone-Release` and `X-Nextgo-Zone-Topology`; protocol value is `1`.

All configured active **and retained** origins are trusted recipients of request
headers, including Cookie and Authorization. Active application responses keep
ordinary cookies and public `X-Nextgo-*` navigation/action headers. Retained
asset responses cannot set cookies: known headers/trailer declarations are
rejected before public commitment, and a late invalid cookie aborts the body
without forwarding it. Hop-by-hop, private identity and forwarded/client-address
headers are stripped; public Origin/Referer and authorization semantics are not
silently replaced. Redirect responses are relayed without following or rewriting
them. Applications must generate correct public URLs.

For a declared HTTPS public origin, native ingress requires actual request TLS
unless `TrustedTLSOffload`/`-trusted-tls-offload` explicitly admits HTTP from a
trusted terminator. That option is an operator assertion, not verification of a
terminator or forwarded header. Restrict the listener accordingly. HTTP public
origins reject TLS requests. This version does not provision certificates,
infer origin/tenant from forwarded headers, implement mTLS/IAM policy, or sandbox
same-origin scripts and native application code.

## Resource and transport contract

| Ingress budget | Default | Allowed maximum |
| --- | --- | --- |
| Exchange timeout | 30 seconds | 5 minutes |
| Request body | 2 MiB | 64 MiB |
| Response body | 8 MiB | 64 MiB |
| Physically active exchanges | 16 | 1,024 |
| Request/response header inventory | 64 KiB | Fixed |

The runtime accepts a trusted `*http.Transport`, clones it, and owns the clone.
It disables environment proxies, HTTP/2, connection reuse, redirects and replay;
unsafe request bodies are not automatically retried or failed over. Custom
dial/TLS callbacks remain trusted native code and must honor cancellation.
CONNECT, WebSocket/protocol upgrades and general tunneling are unsupported.

Request bodies are bounded before upstream dispatch. Response bodies stream
through a bounded relay after identity validation. A known over-limit response
is rejected before commitment. Later overflow/read/write/timeout failure aborts
the response; native HTTP cannot turn that into a clean truncated success or a
new status after headers were sent. Clients must require complete framing/body
and application confirmation. HEAD/304 representation lengths are not body
payload and do not consume the response-body allowance.
The response allowance counts bytes read after HTTP transfer framing; ingress
does not decompress content coding. It is not a decoded browser-memory budget.

Admission remains charged until upstream dial, TLS, I/O and close callbacks,
body closure, and the handler's actual downstream Write/Flush return. Logical
cancellation alone does not free a slot. `Close(ctx)` stops admission, cancels
work and joins physical completion, returning the caller's deadline when needed.
It cannot terminate an uncooperative trusted callback/writer. Custom native
servers should stop HTTP admission/drain the server, then close the ingress or
release with an independent bounded context.

Generated endpoints and ingress select the generic `serverless.Run` adapter
when `AWS_LAMBDA_RUNTIME_API` is configured; otherwise they serve native HTTP.
Buffered/streaming local Runtime API mocks are separate qualification from AWS.
The platform must provide trusted event Host/scheme and preserve the private
backend compatibility headers. Do not treat a synthetic Lambda TLS field as a
native certificate handshake. Adapter/platform limits still apply; increasing
an ingress allowance does not raise a buffered Lambda response limit. No real
API Gateway/Function URL, IAM, TLS/CDN or cloud rollout is qualified here.

## Temporary exclusions and qualification

Independent release v1 requires ordinary production configuration. Development/
HMR, prerender artifacts/policies, locales, the image service and managed metadata
files/factories are rejected. Static public images and ordinary page head
metadata remain normal application content. The asset prefix is fixed to the
release namespace; alternate CDN asset mounts and aggregate static publication
are not supplied. No hot manifest mutation, automatic health/failover/replay,
durable data rollback or coordinated multi-region rollout is implemented.

The sample's counters, typed action, process revisions and marker cookie are
public synthetic data. They do not implement authentication or private tenant
repositories. The intended A1→A2/B1-retained→A1 workflow, paired SDKs, old assets,
HTTP/Lambda streaming, failure/unknown mutation outcomes and ingress restart
must be qualified on the final frozen source; focused contract tests are not
that matrix. New source qualification is pending.

The prior hot-refresh checkpoint `76211b8` retains a failed strict Chromium
BFCache CI result. Its local 334/335 regression and comparative diagnostics do
not establish a cause or a whole-suite fix. The older `bb62d65` green workflow
remains historical and does not qualify this increment. R15 stays **Partial**;
all 79 capability rows and 11 open release gates remain in the parity inventory.
