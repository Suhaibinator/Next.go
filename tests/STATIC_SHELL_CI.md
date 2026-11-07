# Public static shell functional CI integration — source draft

This private packet adds a standalone GitHub Actions workflow and two
development-only Node scripts. It has not been parsed, compiled, tested,
executed, committed or published. No Go/Node/Git command, cache inspection,
browser, port, container, network, AWS or CI job was used to author it. Main,
the repair PR branch, preview PID 27753, prior feature packets and frozen
browser fixtures remain unchanged.

The future feature topic must contain the reviewed framework/sample source
and the original three browser files. The scripts verify those exact pins:

| File | SHA-256 |
|---|---|
| `tests/static-shell-host.mjs` | `a5437350bd6da54ac52a0fadf311505deffc32daf348b2a42db46c75f8921513` |
| `tests/static-shell.spec.mjs` | `7a912c372a7589db4329a3379aca7443057bfdecd4bd8a22480a6f4595127c61` |
| `tests/static-shell.config.mjs` | `fa17f9935fe04125d8b4d1cbb3b37e4684d21840fa156a1b289a4502c9c16078` |

They derive from browser-v2 manifest
`9173e9b8212b2e13030831292295008de8e9267c314a64b96061a6da7c01a319`.
The original private feature source aggregate beginning `6138a69f` is a
separate artifact. This CI records its own actual checkout content aggregate,
commit and files; adding these workflow/scripts necessarily creates a new
source identity. It does not relabel that private packet or claim equivalent
unobserved results.

## Workflow and ownership

`.github/workflows/static-shell.yml` runs on every pull request and pushes to
main, without path filters. Its Go matrix is exactly 1.24.13 and 1.27.3;
matrix failures do not cancel the other SDK. Node is exactly 24.22.1, and
`npm ci --ignore-scripts` consumes the existing Playwright 1.64.1 lockfile.
There are no dependency, runtime JavaScript, Docker or package.json changes.

Each Linux AMD64 job has a 120-minute hard workflow limit. Browser/system
installation has a 60-minute workflow limit (the child supervisor has 59
minutes to leave cleanup margin); this accommodates the reported 47m22s
related successful installation. Production/export has a 35-minute step,
and discovery/browser validation has a 20-minute step. The existing test
configuration itself has its reviewed finite limits. These are future leases,
not measurements of this candidate.

Initialization owns a fresh mode-0700 directory under RUNNER_TEMP and outside
the checkout, marked with exact source/run/job identity. Go/module/npm/browser
caches, temporary files, exports, CLI, logs, generated-helper captures and
reports all stay there. No action cache or preexisting shared build, module,
npm or browser cache is used. SDK provisioning still belongs to setup-go;
the actual selected SDK files are independently pinned in build evidence.
Cleanup accepts only that exact marker-verified subtree, unlocks its own
read-only export directories and removes it. It never selects unrelated
checkout, cache or temporary directories for removal.

Go subprocesses use GOTOOLCHAIN=local, GOENV=off, GOMAXPROCS=1,
GOWORK=off, GOFLAGS=`-p=1 -buildvcs=false`, GOMEMLIMIT=384MiB and telemetry off. GOPROXY
and GOSUMDB are off for these standard-library-only builds. These settings
are not hard RSS/PID/container resource guarantees.

## Actual producer contract

The artifact producer declares exactly seven top-level commands, in order:

1. Selected SDK `go build -trimpath -buildvcs=false -o <owned>/nextgo ./cmd/nextgo`.
2. `<owned>/nextgo generate ./examples/static-shell`.
3. `<owned>/nextgo generate --check ./examples/static-shell`.
4. `<owned>/nextgo export -out <owned>/exports/eager_root ./examples/static-shell`.
5. The same export command into `eager_mounted` with eager mode and `/mounted` base.
6. The same export command into `lazy_root` with lazy mode and empty base.
7. The same export command into `lazy_mounted` with lazy mode and `/mounted` base.

Mode/base are explicit trusted per-command environment values. No additional
Go metadata command is run. The actual selected SDK VERSION file is checked
against GO_VERSION. The selected Go, compile, link and wasm_exec files are
hashed, together with native process platform/architecture and actual Node
version. Each real export's wasm_exec.js must match that SDK shim exactly.

Generation may modify only the four real generator-owned helpers: one route
helper and three action-reference helpers. Their actual emitted bytes are
copied read-only into evidence; none are handwritten or precreated. Check
mode must leave all source file contents/stat identities unchanged. All four
exports must also preserve the post-generation source snapshot. Framework
`.nextgo` scratch and known output/dependency directories are explicitly
excluded from the input snapshot; source files and generated helpers are
included. This is a source-file mutation guard, not arbitrary filesystem
monitoring.

The existing export CLI owns its normal temporary native rendering while
producing genuine HTML/envelopes. The builder starts no separate application
server and does not manufacture HTML, envelopes, bootstrap or WASM. Before
the browser phase, all seven producer process groups must actually be empty.

Each actual export is admitted through the unchanged host validator, including
its actual `.nextgo-export.json` SHA-256, immutable data SHA-256/size and
native HTML identity. A complete bounded file inventory also hashes ordinary
navigation data, seed/home HTML, core, lazy modules, shim and every other file.
Files become mode 0444 and directories 0555. The browser consumer rehashes all
bytes and checks read-only file modes before and after its run, and verifies
that source files remained unchanged.

## Actual consumer contract

Before test discovery, a separately logged runtime metadata probe launches
and closes the three installed browser types to record their actual versions.
It does not create application pages or count as a test case. Playwright JSON
discovery must identify exactly three frozen journeys in each of the sixteen
variant/profile projects: 48 distinct cases for that SDK. This count is a
future assertion; discovery has not been executed here.

Execution uses the unchanged configuration plus explicit workers=1,
retries=0 and max-failures=1. Validation requires the identical 48 discovered
identities, all 48 passing, no skips, no retries, no flakes and no global
errors. Every case must have one artifact-bound actual static-host proof,
including the reviewed shared five-second cleanup deadline and actual
server/socket/handler/file/page/probe joins plus ECONNREFUSED. A report count
alone cannot pass this validator.

The child supervisor records actual PID, stdio close, exit code/signal and
owned POSIX process-group absence. Timeout/output/interruption cleanup uses
one five-second group budget, TERM then KILL if necessary, and reports
unjoined work as failure. Unexpected descendants after nominal child exit
also fail even if terminated successfully. Pending Node workers from a host
cleanup failure must be terminated by this outer supervisor or, if it cannot
join them, by the GitHub job's outer controller. No successful physical join
is invented. This is not complete detached-process/cgroup/resource accounting;
Playwright owns its normal browser/context shutdown, and failed outer cleanup
must fail the job. The private ARM64 full-resource qualification remains held
and separate.

Logs have per-stream byte caps; JSON files and parsing/traversal/attachments
have explicit bounds. An always-run collector publishes at most 64MiB of
bounded actual reports/logs/generated bytes/browser attachments plus a bounded
collection manifest listing omissions. Raw unbounded report paths are never
uploaded. Failure traces/screenshots are retained when available within the
cap. Seven-day workflow artifacts are uploaded before removing the owned root.

These future AMD64 CI results would establish functional behavior only for
their exact sources, SDKs, exports and installed browsers. They cannot close
unverified ARM64 resource/device, provider/CDN, retention, cloud, security,
soak, full SPA parity or release gates. No such results are claimed here.
