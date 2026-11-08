# Independent root layouts

File-routed applications can omit `app/layout.go` and put persistent layouts
under separate `group_name` directories. The first persistent layout in a
page's ancestry owns its document root. A shared `app/layout.go` makes those
pages share one root; templates remount within that root. Pages without a
persistent layout share an implicit root.

```text
app/
  group_marketing/
    layout.go              # marketing root
    page.go                # /
    guide/page.go          # /guide, same root
  group_workspace/
    layout.go              # workspace root
    dashboard/page.go      # /dashboard
    dashboard/settings/page.go
  plain/page.go            # /plain, implicit root
```

Ordinary links within a root use Go navigation and can retain layout/island
state when `PreserveLayouts` is enabled. Links between roots use a full native
document request and initialize the destination's state independently.
`client.Navigate` and `client.Replace` also perform native assign/replace for a
known destination in a different root, preserving their history semantics.
Prefetch rejects these document transitions without fetching their UI. Use
ordinary links for useful navigation without JavaScript or WASM.

The framework derives opaque root identifiers from generated `Route.LayoutIDs`;
no import path is included in the public root field. Manual route registrations
must supply stable, outermost-first `LayoutIDs` to distinguish their persistent
roots. Unidentified manual wrappers share the implicit root. Keep ancestor
identities consistent for routes that share parallel owners. An independent
nested slot layout remains inside its containing page's document.

Multi-root documents contain `data-nextgo-root`; route manifests and navigation
envelopes carry matching identities. Route selection binds the response root
after rewrite/interception selection and outside materialized cache snapshots.
A conditional rewrite to another root makes the browser reject the envelope
and request a new document before installing its UI. Buffered and streamed
navigation use the same check; streamed HTML also declares the selected root.
Unknown paths in a multi-root application use native requests. A global unmatched
document gets its own identity. Single-root applications omit these fields and
retain their existing wire shape.

Base paths, locale scope and the document's asset prefix retain their existing
ownership rules. Static publication preserves root boundaries and verifies that
each HTML document and its navigation data have the same root before atomic
publication; a mismatched export preserves the previous publication. Root
identities are compatibility values, not authorization or confidentiality.
Every loader/endpoint still needs its ordinary access checks.

The [root-layout example](../examples/root-layouts/README.md) demonstrates two
explicit roots, an implicit root, Go counter retention, native Navigate/Replace,
and a conditional rewrite. Its static-export phase omits the server-only
conditional rule and link. The streamed demonstration page is outside the
selected static export, because plain static hosting cannot execute streaming.

The [qualification record](benchmarks/root-layouts-qualification.json) pins the
source and artifacts. Go 1.24.13 and 1.27.3 each passed 80 native race-tested
packages, native/WASM client vet, nine WASM package runs across production/core,
production/module and both development-refresh targets, 19 Chromium cases over
HTTP/buffered Lambda/streaming Lambda/static hosting, and 42 compiled deployment
cases on the host and again in an isolated Linux container. A fresh single-root
demo also passed 46 private-route/font/client-only Chromium regressions on
Go 1.27.3. The CI definition requires 76 cases across 16 profiles per SDK;
unexecuted browser engines and actual cloud behavior are not qualified here.

This remains an implemented routing subset. Broader root/locale/parallel/
interception/prerender/rollout combinations, naming/diagnostics and production
requirements remain in [PARITY.md](PARITY.md). The reference demo still exceeds
both existing WASM size budgets on both SDKs; this checkpoint closes no gate.
