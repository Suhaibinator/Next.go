# Current advisory qualification

The [November 7 record](benchmarks/govulncheck-local-database-qualification.json)
contains ten completed `govulncheck` v1.9.0 checks against the pinned current Go
source and actual demo/root executables. This is known-advisory evidence. It is
not a source security audit, independent exploit confirmation or release approval.

| SDK | Native source | Core/module WASM source | Actual demo/root ELF binaries |
| --- | --- | --- | --- |
| Go 1.27.3 | No known findings reported | No known findings reported | No known findings reported |
| Go 1.24.13 | 37 advisory IDs with reachable symbols | 2 advisory IDs with reachable symbols in each configuration | 42 advisory IDs with linked symbols in each binary |

The legacy SDK has 43 affected module-level advisory IDs overall. Module,
imported-package, reachable-symbol and binary-linked-symbol results are different
levels of evidence; repeated call traces are not distinct vulnerabilities.
The WASM symbol results include
[GO-2026-4601](https://pkg.go.dev/vuln/GO-2026-4601) (IPv6 host parsing in `net/url`)
and [GO-2026-6218](https://pkg.go.dev/vuln/GO-2026-6218) (quadratic `resolvePath`).
The report preserves all IDs, summaries, fixed-version metadata, scopes and raw
output hashes without asserting a confirmed exploit in an application.

The production Dockerfile already pins Go **1.27.3** by image digest. Go 1.24 is
the minimum language/compatibility target, outside upstream support and not
security-qualified for production. Its functional test passes do not waive the
standard-library findings. Keep production SDKs patched and qualify their Go
core/modules/bootstrap as one generation. See [deployment](DEPLOYMENT.md#build-toolchain-policy)
and [compatibility](COMPATIBILITY.md#compiler-binary-and-browser-boundaries).

The later [widget checkpoint](benchmarks/widgets-qualification.json) repeats
native/core/module source checks after the lifecycle API was added, with no
known findings on Go 1.27.3. Its narrower widget-component WASM roots report
one reachable Go 1.24 advisory ID (`GO-2026-4601`), while native still reports 37.
Those scopes do not replace the earlier demo-component and ELF results above.
The pinned uPlot 1.6.32 npm advisory query reports zero known findings, and the
official vulnerability database Git HEAD was reconfirmed unchanged. These are
automated advisory observations, not independent source review, library safety
approval or a closed G02 gate.

## Official local database provenance

Direct access to `vuln.go.dev`, GitHub's API and the module proxy's archive-storage
redirect was denied in this environment. The allowed official GitHub codeload
archive was reconstructed with `golang.org/x/mod/zip`; normal Go module download
with `GOSUMDB=sum.golang.org` authenticated the entire module ZIP. No checksum
exemption or framework dependency change was used. Two generator dependencies
whose official archive redirects were also denied were authenticated the same
way. The package-manager allowlist and ordinary inherited proxy/TLS trust were
preserved.

The official module is
`golang.org/x/vulndb@v0.0.0-20261105234957-e22db6528018`, checksum
`h1:mfngRv0Yoh0VApsXKEN82CjD99kl7mtparN3bs3JnqI=`. The observed official repository
HEAD was `e22db6528018f9725b3230c4e6429c791f40bb15`. The unmodified official
`cmd/gendb` used its Git history to produce 4,789 OSV entries / 9,584 files, with
database modified time **2026-11-05T23:49:57Z**. The report pins the complete file
manifest, tool binaries/build information, archive, module verification and HEAD
observation. It does not claim the inaccessible live deployed API has identical
state or that no later advisory can exist.

For an ordinary environment with permitted public downloads, install the pinned
official generator and use a full-history repository checkout at the recorded
commit. Repository OSV JSON has zero timestamps; `gendb` derives authoritative
publication/modified dates from Git history. Do not replace them with today's
date or treat raw repository JSON as an already deployed database.

## Repeating the checks

Select the production SDK before installing/running the checker:

```sh
go install golang.org/x/vuln/cmd/govulncheck@v1.9.0
govulncheck ./...
GOOS=js GOARCH=wasm govulncheck ./client ./mutation ./offline ./offline/worker ./internal/navigation ./internal/routing ./ui ./examples/demo/components/...
GOOS=js GOARCH=wasm govulncheck -tags=nextgo_module ./client ./mutation ./offline ./offline/worker ./internal/navigation ./internal/routing ./ui ./examples/demo/components/...
```

For the locally generated snapshot, add `-db=file:///absolute/path/to/database`.
Binary mode checks a built executable with `-mode=binary`. Source checks exclude
tests here; native scope is `./...`, while WASM scopes include the shared browser
libraries and demo components. ELF scanning does not analyze its embedded WASM.

JSON mode returns zero even when it emits findings. The pinned checker's
`-mode=convert` converts the recorded JSON to text and returns **3** for the
legacy-SDK findings and **0** for the production-SDK results. CI now declares
separate pinned production-SDK native/core-WASM/module-WASM jobs against the
live official database and uses this nonzero findings gate. Its YAML and local
equivalent were checked; remote CI was not run here.

The Codex Security workflow still cannot start its required ready preflight:
its shared assets and scan context/tool are unavailable. No current canonical
source-audit report exists. G02 and all other release gates remain open in
[PARITY.md](PARITY.md); no user acceptance or waiver is inferred.

## Native ESM checkpoint

The [ABI-6 module-import qualification](MODULE_IMPORTS.md) repeats native, core
WASM and lazy-module source advisories for Go 1.27.3 and Go 1.24.13 against the
same authenticated official database, whose latest official HEAD was reconfirmed
as `e22db6528018f9725b3230c4e6429c791f40bb15`. Conversion exits are 0 for all
three production-SDK scopes and 3 for all three compatibility-SDK scopes.
The latter retain 37 reachable native advisory IDs and GO-2026-4601 in each
widget WASM scope. No advisories were waived. Native import uses document CSP
and browser module policy; it provides no transitive-graph SRI or JavaScript
sandbox. The canonical full source audit and G02 remain open.

## Script data settings checkpoint

The [script-settings qualification](SCRIPTS.md) repeats the three source advisory
scopes on each SDK, with all production conversion exits 0 and all compatibility
exits 3. Official database HEAD and stable Next.js baseline were reconfirmed.
The new settings reject reserved/executable attribute names, non-string/null or
duplicate JSON values, controls/invalid UTF-8 and oversized encoded input.
Native rendering escapes values; managed element loading sets them before src.
This is trusted public vendor configuration, not a library sandbox or a
replacement for the still-open source audit and G02.
