# Hosted client-only browser qualification

The dedicated `client-only` CI job runs the existing five client-only scenarios
in Chromium, Firefox, WebKit and WebKit touch: **20 executions per SDK**. The Go
1.24.13 and 1.27.3 jobs each build their own real CLI and production demo with
Node 24.22.1 and the lockfile's Playwright 1.64.1. This is Linux AMD64 hosted
functional qualification, not evidence for ARM64, deployment platforms, a memory
limit or a cgroup profile. Source review of this job alone is not a passing run.

The Ubuntu 24.04 runner uses its actual nonroot UID. Setup actions install the
pinned SDK and Node; `npm ci --ignore-scripts` installs locked dependencies, and
the Playwright installer has a 60-minute limit. The job has a 210-minute limit;
the existing build and client-only qualification steps each have 25-minute
limits. The unchanged browser config has a 20-minute campaign timeout, one
worker, no retries and fail-fast
after the first failure. Command supervision also has finite deadlines and
bounded output. These limits do not establish per-process CPU or RSS bounds.

The original four phases of `scripts/client-only-ci.mjs` are:

1. `init` verifies a fresh mode-0700, current-UID root under `RUNNER_TEMP`, outside
   the checkout. It creates task-owned compiler/module caches, temporary paths,
   artifact and evidence directories. It records source hashes and actual Git
   checkout identity. Source bytes (including the request-gate dependency) and
   Node's executable pin are rechecked before discovery and after the matrix.
   No shared cache, HOME, credentials or Git trust is changed.
2. `build` checks the actual SDK version, builds `./cmd/nextgo` using
   `-trimpath -buildvcs=false`, checks generated demo references, and runs the
   CLI's production `build -out ... ./examples/demo`. The fresh CLI and server
   become mode-0500 files. The server's SHA256 is exported to the existing
   prebuilt-only browser fixture; there is no fallback producer.
3. `qualify` requires discovery of exactly the five original titles in all four
   profiles, then runs the original config/spec once through the existing
   browser wrapper. Every expected case must pass exactly once, with zero
   retries, skips, unexpected results, flaky cases or report errors. The helper
   checks all 20 inline `client-only-native-process-and-wire-proof` attachments.
4. `collect` publishes a bounded selection of evidence even after failures.
   The hosted artifact contains source/version pins, command receipts/logs,
   discovery/results, qualification or failure receipts, and selected browser
   traces/screenshots/error context. It excludes compiler/module caches, native
   executables, temporary profiles, HOME, source trees and dependencies. It
   records copied hashes and omissions: 128 MiB of copied payload bytes (excluding
   collection metadata and upload overhead), 32 MiB per file, at most 512 files,
   4,096 visited entries and depth 16. Directory reads materialize their entries
   before that visit cap is applied. It rejects symlink files.

The shared fixture continues to execute the pinned production server from a
fresh owned directory outside the checkout. Its gateway forwards actual native
responses. Its optional local metadata data source must remain unused in these
client-only cases; the gate requires an empty source request list. No synthetic
application server or substituted application HTML/stream response is used.

Each receipt must identify the original server path, hash, size and read-only
mode. The fixture observes the **actual tracked native PID while ready**, not a
separate UID probe. On Linux it reads bounded procfs status and before/after
start ticks, verifies the Node parent PID, rejects dead/zombie states, and
records numeric UID/GID values. The CI gate requires all four native UIDs to
equal the actual nonroot parent UID. Other platforms report this procfs
observation as unsupported; they do not acquire fabricated Linux evidence.
The 64 KiB per-proc-file limit bounds bytes, not independent wall time. Existing
case/startup/job deadlines remain the surrounding time bounds.

All 20 receipts must show graceful TERM, actual native close/stdio join, code 0,
no signal, no forced kill or unexpected exit, both real gateway/data-source
listeners joined and closed without errors, and zero remaining sockets and
upstreams. The owned native cwd is marked removed only after successful `rm`
and a subsequent `ENOENT` check. The gate independently checks its absence,
original PID absence (or a different start identity after PID reuse), unchanged
server bytes, and no listeners on ports 4160–4162 after the campaign.

The controller captures bounded stdout/stderr from its own detached command
group and waits for `close`, not only the wrapper's `exit`. A successful command
must also leave no member of its owned process group. Timeout/log overflow sends
TERM, then KILL after 10 seconds, with a further five seconds to observe close.
A missing physical join or remaining group is a failure, never a cleanup PASS.
The original wrapper and test teardown timings are not changed by the proof
enrichment.

The successful campaign additionally matches every observed actual Playwright
browser launch PID to an exit-0/null-signal DEBUG receipt. This covers browser
processes that may use separate groups. Collection always attempts a bounded
selected-port and source-pin observation, including on failure. A selected
listener left behind makes the job fail. If a worker dies before its native
fixture attachment exists, this helper cannot prove or independently reap that
detached child: it does not signal unidentified processes or claim successful
failure cleanup. Controller death can leave that child until the hosted job's
outer cleanup; this job does not claim that cleanup was observed. Hosted runner
destruction is a separate platform boundary. Isolated failure-ownership/resource
qualification remains open; this functional job does not close that gap (G06).

Playwright package/browser definitions and actual `pw:browser` launch entrypoint
hashes are captured. This includes Chromium's selected headless shell rather
than assuming `chromium.executablePath()` is the executable used. WebKit's
entrypoint may be its shipped launcher; its hash is not a checksum of every
browser distribution file or every descendant executable.

The fixture's additional identity and cwd fields preserve its existing cleanup
behavior and consumers' assertions. This new fixture source has its own
hashes. It does not inherit the evidence or authorization of an older frozen
local ARM64 packet, and does not bypass that packet's execution prerequisites.
Metadata-stream, metadata-clear, adapter, cloud and other suites retain their
separate qualification boundaries. No default test selector changes here.

## Incremental metadata coverage

The same per-SDK job now adds three sequential phases after the existing
client-only phase. There is still exactly one CLI/demo producer per SDK and no
cross-job artifact transfer. Every phase reuses the pinned, read-only production
server, checks source and Node identity, and verifies server bytes afterward.
The original client-only phase, its config/spec, and all its time bounds remain
unchanged. The shared native-proof validator retains all original default
client-only assertions and also accepts the existing metadata attachment name.

`metadata-stream` discovers the existing eleven titles in all four profiles and
requires exactly 44 passing cases. `metadata-clear` does the same for two titles
and eight cases. Each uses its unchanged config with its existing 20-minute
global timeout, a separate 25-minute workflow step, and a 22-minute command cap.
Discovery, results, qualification receipts and browser output directories are
separate, so a later suite cannot erase earlier evidence. Every result must be a
single passing attempt, with no skips, retries, flakes or report errors.

All 52 additional browser cases must attach their actual
`native-data-source-and-wire-proof`. The shared fixture already produces actual
native UID/start/parent identity and directory-removal evidence. The CI gate
requires the same native, listener, socket, process and artifact guarantees as
client-only. Metadata data-source requests are allowed only when explicitly
armed; the existing speculative 204 case must leave that source unused. All
observed Playwright browser PIDs must have successful exit receipts.

`metadata-deployment` runs the existing Node test module once, serially, using
`NEXTGO_METADATA_DEPLOY_BINARY` and `NEXTGO_METADATA_DEPLOY_SHA256` for the same
demo. Its TAP gate requires the exact five families for each of HTTP, buffered
Lambda and streaming Lambda: 15 passing results, no skipped/todo/canceled/failed
tests, and exact plan/count/title matching. There is no non-executing Node
discovery claim. The existing per-case 45-second limit remains; the command has
a 13-minute cap and its workflow step has a 15-minute cap.

Each Node case awaits the original fixture's cleanup assertions, including real
native PID/group disappearance, source/application listener closure, sockets,
owned environment directory removal, unchanged artifact, and Lambda gateway
join when used. That fixture does not emit durable per-case UID/process
receipts. A passing TAP case proves its assertions completed; this CI helper
does not invent fifteen external receipts or claim observed native UIDs there.
Always-run collection additionally observes ports 4160–4162 and 4280; dynamic
Node source ports are checked inside the existing fixture, not inferred from
that fixed-port observation.

The job cap increases only to accommodate these new stages. Existing explicit
step caps plus the new ones total 188 minutes; 210 minutes leaves finite room
for setup and collection. No timeout is hidden or retried. Collection keeps its
existing payload/file/entry limits across all three browser directories, so
large failure evidence can be omitted with a recorded reason.

This source extension remains unexecuted until separately qualified at its own
commit. Earlier client-only passes at the actual 35a3 merge and the historical
metadata Go1.27 44-case outer-controller failure remain distinct evidence; this
proposal does not convert the latter to success. G06 and the controller-death
ownership boundary above remain open. These additions close selector gaps only
when their real hosted runs and bounded evidence pass; they do not establish
full platform maturity.
