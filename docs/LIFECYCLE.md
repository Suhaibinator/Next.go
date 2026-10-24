# Native history and preview restoration

Browser back/forward cache can retain an entire document and its JavaScript
heap, including the running Go/WASM instance. This differs from Go soft
navigation, which fetches rendered content and retains supported layout scopes.
Native cache eligibility belongs to the browser; `no-store` does not provide a
portable promise that a document will never be restored from memory. See the
[browser lifecycle guide](https://web.dev/articles/bfcache) and
[Chrome's no-store policy](https://developer.chrome.com/docs/web-platform/bfcache-ccns).

## Managed preview behavior

Applications with `Config.Draft` use a small bootstrap bridge for persisted
`pagehide`/`pageshow` events. It marks the framework application tree as frozen
before the browser retains or reveals it. The bridge runs independently of WASM
startup. If Go has not started, a persisted restore loads a fresh document.
The bridge contains no signing keys, component state or mutation policy.

Once Go is running, restoration proceeds as follows:

1. Keep the retained tree hidden. Cancel superseded navigation, transitions,
   speculative requests and any older restore check.
2. Verify the session through the deployment/locale-scoped
   `GET /_nextgo/draft` endpoint, after leaving the synchronous activation task.
   The request uses same-origin credentials, no-store fetching, redirect
   rejection, a five-second deadline and a 1 KiB response limit.
3. Require an empty 204 response with the exact endpoint URL and matching
   navigation protocol, build, configuration and preview-state headers.
4. If compatible, reveal the existing DOM and keep its Go state/listeners.
   Restore connected focus without scrolling and retain the prior scroll for
   the same URL. A pending refresh or changed native-history target proceeds
   after verification.
5. If the session or generation changed, load a fresh document before revealing
   the retained tree. Transport failures keep it hidden and offer a scoped
   **Retry verification** button. With an open native dialog, the public status
   temporarily lives inside its focus boundary; private modal contents stay
   hidden. Success returns the status to its original location and restores
   connected focus. Superseded results cannot reveal the tree later.

The endpoint returns no CMS records, cookie values, identities or keys. It is
private/non-cacheable and does not execute page loaders. HEAD is supported;
other methods return 405. Unconfigured apps and public asset-only origins do
not expose a usable probe. `draft` is reserved as a framework runtime namespace.

This check verifies preview capability state and deployment compatibility. It
does not verify arbitrary user identity changes or refresh CMS content.
Application data still needs authorization and reconciliation. Outstanding
component mutations are not automatically retried; cancellation does not prove
that a server write failed to commit.

## Go and browser constraints

The native cache keeps Go memory and scopes alive; restoration does not remount
islands or deserialize a second state tree. Browser suspension is separate from
component unmounting, so this is not a general effect pause/resume API. A fresh
document initializes new Go state normally.

The runtime avoids History API writes while a page is hidden or freezing.
Scroll/focus needed for restoration are captured in Go memory; ordinary active
scroll events own history bookkeeping. Network, focus and scroll work resume
outside the synchronous activation event. A frozen document rejects new
programmatic navigation, skips prefetch and defers `Refresh` until verification.
Configured preview applications also skip speculative page requests because
their responses cannot be reused safely across HttpOnly session changes.

The freeze presentation uses the framework-owned stylesheet emitted by the
document renderer. Application CSP must permit that stylesheet. Complete strict
CSP, physical-device and screen-reader qualification remains part of the open
production gates. If JavaScript/bootstrap cannot execute, native history follows
browser HTTP/cache policy; SSR content and ordinary forms still work.

Browsers may decline to cache a private page or evict a retained document for
resource or lifecycle reasons. Keep privacy headers: a fresh SSR response is the
correct fallback. Whole-route Go caching across unrelated document loads is not
implemented by this feature.

## Evidence and test commands

`tests/restore.spec.mjs` dispatches deterministic lifecycle events to test the
Go/bridge contract on Chromium, Firefox, WebKit and touch WebKit. These are not
claims that a browser froze its heap. They cover pending checks, mismatched
metadata, retry/listener ownership, superseded requests, pre-WASM fallback,
named-property interference, mounted locale endpoints and keyboard recovery
inside an open native modal without remounting its Go state.

`tests/bfcache.spec.mjs` exercises actual Chromium history across a site boundary.
Its published-page case requires `pageshow.persisted`, the same DOM node, retained
Go counter state, focus and scroll, and a completed server probe. A fresh reload
cannot satisfy that assertion. The private case validates retained state when
the browser caches it and fresh private SSR when the browser declines it. A
separate native case verifies cookie removal while away cannot expose the old
preview page.

The native tests launch full Chromium with Playwright's default cache-disabling
argument removed and Chrome's documented no-store support flag. They avoid
request interception and tracing during the native case, which can alter frozen
renderer behavior. Failed eligibility checks attach browser reasons. The
deterministic suite retains ordinary Playwright traces.

```sh
node scripts/test-browser.mjs --project restore --project bfcache
sh scripts/test-crossbrowser-container.sh --project chromium-bfcache --repeat-each=3
```

`PLAYWRIGHT_CHROMIUM_FULL_EXECUTABLE_PATH` optionally selects an installed full
Chromium binary for these native tests. It is separate from the headless-shell
override used by the rest of the host suite. Native HTTP and both mock Lambda
fixtures verify empty probe bodies and session/build/configuration headers
before and after preview changes. Mock transports do not establish real-cloud
or complete production qualification.
