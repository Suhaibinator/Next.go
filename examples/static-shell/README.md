# Explicit public static client shell

This isolated example has an ordinary home page and one declared optional
catchall, `/workspace/*rest?`. The declaration exports only the generic
zero-tail workspace seed. Links such as
`/workspace/notes/caf%C3%A9?tag=one&tag=two#details` and
`/workspace/boards/team?sort=recent` deliberately have no separately enumerated
page export. There are no layouts, slots, templates, selection snapshots,
application APIs, private records, actions or application JavaScript.

`Configure` reads trusted operator configuration before requests:

- `NEXTGO_STATIC_SHELL_MODE=eager` (the default) selects `workspace`.
- `NEXTGO_STATIC_SHELL_MODE=lazy` selects `workspacelazy`.
- `NEXTGO_STATIC_SHELL_BASE=/mounted` optionally places the whole artifact
  under a literal deployment prefix.

Both components remain discoverable in either mode. Exactly one selected
`ui.ClientIsland` is rendered, and the declaration uses that same component
name. The lazy component delegates to the same browser-only Go view. Native
startup settings and native page packages do not enter its browser import
graph. This source uses the repository's module and standard library only.

The native page never reads request parameters, query, headers, cookies or
private state. Its title, description, props and fallback text are public
constants. Deployment URL placement changes links through the normal URL
helper; it does not put the current route or operator environment into props.
The fallback includes a heading, editable native input and ordinary links.
It remains readable before WASM, with scripting disabled, or after a failed
mount. There is intentionally no `StaticParams` enumerator, so the producer's
explicit zero-tail seed inclusion is exercised.

`Mount` calls `CurrentLocation` and `CurrentRouteParams` before calling
`ClientView`. The view shows the actual escaped pathname, decoded optional
catchall, decoded query values (including repeated values), decoded fragment,
and the rest captured during Mount. The same Go code runs in eager and lazy
targets. A scoped location observer updates hash-only changes; a Go counter
and listener belong to the view. Cross-page and query navigation remount the
view and reset the counter. Leaving for home disposes the view. The Replace
button uses the existing Go history API.

The client recognizes workspace root, public notes and the team board. Other
owned subroutes show an explicit client missing-screen. Every eligible deep
document request still serves generic HTTP 200; this is not native per-record
404 or per-route SEO. The app publishes no deep-specific metadata or data.
Unknown paths outside `/workspace` require actual static 404 responses.

## Static host publication contract

Use one fresh paired CLI/framework/core/lazy artifact. Its
`.nextgo-export.json` must have `format: "nextgo-static-v3"`, normal build,
configuration, paths and deployment fields, and one `staticShell` descriptor.
The descriptor's seed, component, exact byte count and SHA-256 bind the
immutable envelope at
`<base>/_nextgo/<build>/static-shell/<sha256>.json`. Every exported HTML
document carries the same descriptor in `data-nextgo-static-shell`.

A static host must serve exact retained files first with their correct MIME
and status. Only eligible GET/HEAD document requests in the declared subtree
may use the seed HTML, retaining the visible URL. API, reserved namespace,
missing JSON/WASM/CSS/JS/data and other resource requests never receive HTML
fallback. Mutation methods and unknown outside paths cannot select the shell.
Export does not configure a CDN or prove provider behavior.

## Source-only status

This sample is staged in a private recovery overlay. It has not been applied
to the main tree, built, exported, tested or opened in a browser. The static
fixture/browser proof is a separate follow-up using reviewed prebuilt exports;
there is no application server or Go compiler in that browser-run contract.
No payload, compatibility, four-engine, lifecycle, deployment or parity result
is claimed by these source files.
