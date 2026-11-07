# Source-only prebuilt static shell browser fixture

This is immutable derivative v2 of the unexecuted private browser overlay.
The original four-file browser freeze and sample freeze remain unchanged.
No server was started and no Node,
Playwright, Go compiler, application server, browser, network, AWS, container
or CI job was run to author it. Main source and preview PID 27753 were not
changed. This document describes future finite proof, not observed results.

The accepted sample freeze is
`static-shell-sample/source-manifest.json`, SHA-256
`169626ca03d2c3cd3a489d035a68f6160d1c54153617f54560d4278abe3cfed2`.
This fixture uses the separately root-approved finite resource policy from
portable interface v2, SHA-256
`88e03b8cd455670c1af38015727e97788814662612a3a8ed20dce30cfc1a2654`.
The earlier sample/spec freeze is preserved; this derivative supersedes its
unresolved general file-like path policy for these new fixture files.

## Explicit inputs

The host reads only an explicit canonical absolute export directory, whose
symlink-free path must equal its filesystem realpath. It requires the exact
SHA-256 of that directory's `.nextgo-export.json` bytes. There is no default
artifact, source application selection, native executable, compilation,
export, download or subprocess path in the fixture.

Supply each variant's two environment values before a separately approved
run:

| Variant | Export directory | Exact manifest SHA-256 |
|---|---|---|
| eager/root | `NEXTGO_STATIC_SHELL_EAGER_ROOT_EXPORT` | `NEXTGO_STATIC_SHELL_EAGER_ROOT_MANIFEST_SHA256` |
| eager/mounted | `NEXTGO_STATIC_SHELL_EAGER_MOUNTED_EXPORT` | `NEXTGO_STATIC_SHELL_EAGER_MOUNTED_MANIFEST_SHA256` |
| lazy/root | `NEXTGO_STATIC_SHELL_LAZY_ROOT_EXPORT` | `NEXTGO_STATIC_SHELL_LAZY_ROOT_MANIFEST_SHA256` |
| lazy/mounted | `NEXTGO_STATIC_SHELL_LAZY_MOUNTED_EXPORT` | `NEXTGO_STATIC_SHELL_LAZY_MOUNTED_MANIFEST_SHA256` |

Also provide `NEXTGO_STATIC_SHELL_LEASE_MS` as an integer between 1000 and
120000. It is the maximum life of each owned in-process static listener;
tests require cleanup before lease expiry. `NEXTGO_E2E_RESULTS` can select
the report directory. Existing Playwright dependencies and already-installed
browsers are prerequisites. The optional existing
`PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH` selects Chromium's executable.

The isolated `static-shell.config.mjs` imports no other configuration and
has no `webServer`, setup build hook or global application server. It defines
four browser profiles (Chromium, Firefox, WebKit, WebKit touch) for the four
artifact variants. A future approved runner may select a finite subset using
ordinary Playwright project filters. Existing repository Node/Playwright
dependencies are used; no npm change or package installation is needed.
The root qualification runner will separately bind exact artifacts from each
approved SDK. No discovered test totals or passes are asserted here.
The configuration explicitly sets `maxFailures: 1` and `retries: 0`.

## Actual artifact admission

Before listening, the host recursively inventories every retained file. It
refuses symlinks, hardlinks, special files, path aliases, excessive directory
depth and excessive file/count/total sizes. Files are opened read-only without
following the final symlink, hashed and size-checked. The manifest's actual
bytes must match the supplied pin. The publication must use
`nextgo-static-v3` and protocol 3, with one unlocalized scope, valid build and
configuration identity, and exactly the sample's home and zero-tail seed.

The actual version-1 staticShell descriptor must name the selected component
(`workspace` or `workspacelazy`), declare `/workspace/*rest?`, derive the exact
mounted seed/envelope URLs, and match the retained immutable envelope's byte
count and SHA-256. That actual envelope must have the seed URL and matching
normal navigation identity plus the single matching native island. Both
retained HTML documents must have matching build/configuration/export markers
and the exact descriptor. Core and lazy module WASM must actually exist.

This intentionally narrow sample harness is not a replacement generic export
manifest parser or production host. Its input manifest is pinned producer
output. In addition to the manifest pin, it records a deterministic digest of
its actual all-file inventory. Each response reopens and rehashes the selected
retained file against that inventory. It cannot silently serve changed bytes.

## Frozen host policy

Raw request path validation precedes URL normalization and exactly-once
decoding. Invalid escapes/UTF-8, empty or dot segments, decoded separators,
backslashes and controls fail closed. Literal mount/prefix spelling and one
permitted terminal slash follow the agreed browser/portable policy. Query
does not influence file or shell selection; fragments never reach the server.

The decision order is GET/HEAD method gate, valid literal mount and literal
shell/framework namespace prefixes, exact retained
files or declared ordinary documents, then virtual shell admission. Exact
files retain their native MIME and bytes. A virtual fallback additionally
requires an HTML document Accept value, a document destination when supplied,
and the exact shell prefix. API/reserved/missing runtime/public resources do
not select virtual HTML fallback. Unsupported methods are terminal 405;
unknown paths and excluded resource requests are non-HTML 404/400. HEAD has
the corresponding GET headers and no body.
The literal namespace guard runs before exact-file lookup: encoded aliases
such as `/work%73pace/index.html`, `/%5fnextgo/<build>/app.wasm` and the
equivalent immutable-envelope alias cannot bypass it. Encoded basenames
after a literal namespace, such as `/_nextgo/<build>/app%2Ewasm`, still map
to their exact retained file. The negative journey uses actual retained
seed/core/envelope paths to exercise this boundary.

The root-approved virtual resource classifier denies exactly these final
decoded segment suffixes, ASCII case-insensitively:
`.json`, `.wasm`, `.css`, `.js`, `.mjs`, `.cjs`, `.map`.
It runs after exact-file/document precedence and one terminal slash removal.
Thus `report%2EJSON` and `report.json/` are denied; `notes.v2`,
`notes.json/details`, and literal-percent `report%252Ejson` remain eligible
document paths. It does not use all extensions as a blanket denial. Generic
`Location.RouteParams` remains unrestricted by this publication-only policy.
This is a bounded partial resource policy; it does not claim that every
possible extension represents a forbidden resource.

## Three focused journeys per configured variant/profile

1. Direct unexported UTF-8 URL: with scripting disabled, actual generic HTTP
   200 fallback has a meaningful heading, editable native input and home link.
   With the actual runtime enabled, eager or lazy Go mounts at that URL,
   reports decoded rest/repeated query/hash and Mount-time rest, and increments
   its scoped counter. Actual core/module requests supply WASM evidence.
2. Actual Go SPA: exported home enters the unexported path; immutable shell
   requests replace target-specific missing JSON. Go history, back/forward,
   Replace, query remount, fragment observer, missing-screen and leave/reentry
   retain one document and have explicit counter reset/preservation behavior.
3. Negative publication and host policy: deliberate immutable 404, byte
   corruption and wrong MIME each preserve the exact current Go view, URL,
   counter and document. Restoring genuine bytes allows later navigation.
   Grouped API requests verify terminal resource/API/method/mount denials,
   all seven suffixes, encoded spellings, allowed dotted paths, document
   Accept/destination guards, HEAD, and exact retained JSON bytes/MIME.

Faults exist only in HTTP response handling, scoped to the actual immutable
envelope path. Missing and MIME faults change response status/type; corruption
copies and flips one actual byte. No synthetic successful application HTML or
envelope is constructed, and approved artifacts remain read-only.

Each journey owns an ephemeral loopback listener in its Playwright worker.
The fixture records bounded actual method/target/status/MIME/served-file/fault
logs, actual artifact pins and inventory digest. Cleanup closes the test page,
joins the server close callback, all owned socket closes and all asynchronous
file-serving handlers (including actual file-close completion), plus the
owned test page's close promise, and requires no active response streams.
One absolute five-second deadline starts at cleanup entry and covers all
these joins and the actual post-close TCP refusal probe. There is no fresh
per-phase budget. Success requires observed callback/socket/handler/file/page
joins and an actually closed probe receiving ECONNREFUSED.

On deadline expiry, close rejects and records actual pending sockets,
responses, handlers, filesystem operation phases and probe state. Winning
a Promise race never marks pending work as joined. Unsettled filesystem
operations cannot reliably be canceled by this fixture; its proof explicitly
requires the outer qualification controller to terminate the Node worker on
timeout/failure and verify that process physically exits. This fixture does
not manufacture that outer-process evidence. Startup filesystem admission is
also subject to the outer controller's finite lease. Cleanup proof is attached
even when a journey fails. There is
no native child process, so no native-process exit proof is invented.

## Limits and remaining proof

These files have not been parsed or run by Node, discovered by Playwright,
bound to an actual export, or tested. Their syntax, paired-source integration,
generated component discovery, browser behavior and physical cleanup still
require separately authorized validation. This small fixture does not cover
all cancellation/epoch races, delayed abandoned lazy loads, incompatible
generation rollback, descriptor malformation, asset retention, provider/CDN
configuration, long-run resource stability or arbitrary CSS/asset MIME types.
The prior broader source plan remains a backlog for those bounded proofs.
No feature inventory row or release gate closes from source authorship.
