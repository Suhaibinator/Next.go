# Experimental retained-route slice

This private example uses `Config.RetainRoutes` and per-response
`Page.Retention` to preserve a small eager page's Go state and exact DOM during
buffered history traversal. It needs the accompanying private runtime source.

```sh
go run ./cmd/nextgo build -out ./dist/retained ./examples/retained
./dist/retained/server -addr 127.0.0.1:4188
```

Open `/a`, edit the draft, increment, open details and navigate to B, then Back.
Hidden effects stop and restart when visible. A canceled task is not replayed.
A return always contacts the server; the first slice restores only an unchanged
server-body fingerprint and public privacy partition. Changed server output
renders fresh, resetting page state. State-preserving reconciliation remains
mandatory future work; this example does not complete V10.

A/B/C/D demonstrate the three-view bound. `/owner` makes the sole ordinary
route owner itself an eager island and scroll container. `/broken` deliberately
fails that owner’s mount and must report the error without retaining it. `/ids` deliberately contains an author
ID and uses ordinary fresh rendering rather than risking hidden duplicate IDs.
`/unsafe?kind=details|anchor|radio|form|form-association` exercises native
document-wide ownership cases that this slice also cold-renders.
`/api/control` changes only demonstration cookies for partition/version/denial;
it is not authentication or a grant to private data. Never put session secrets
in the public partition. No lazy/slots/streaming/draft/offline or retained shared
layouts are supported by this first slice. Existing applications default off.

Stop the manually started server before running this command. The fixture
starts and owns its own server on `127.0.0.1:4188`, so that port must be free.

The browser suite requires an explicitly built native artifact and SHA256; it
has no build fallback or synthetic application responses:

```sh
NEXTGO_RETAINED_SERVER_BINARY=/absolute/path/to/dist/retained/server \
NEXTGO_RETAINED_SERVER_SHA256=<sha256-of-that-binary> \
npx playwright test -c tests/retained-routes.config.mjs
```

The task button makes an actual bounded HTTP read. Hiding its owner cancels the
request; server metrics prove cancellation and that reactivation does not replay it.
No command above was executed while preparing this source-only candidate.

`/a?retire=1` opts into a terminal cleanup that starts `/c?retired=1`; the
retirement regression changes partition before B, then aborts that real newer
request at the network boundary. B must already be hydrated and stay interactive
after the recoverable failure. Retained hiding alone never runs terminal cleanup.
`/a?resumeFail=1` deliberately fails its second managed-effect setup, testing a
persistent generic hydration error, disposed component listeners, and recovery
through the global router to an ordinary fresh A. Both controls are example-only
fault injection, default off; they do not expand the retention feature contract.

`/a?cleanupPanic=1` opts into a terminal cleanup panic. Navigating to the
unsupported `/ids` destination must still commit its authorized fresh HTML and
hydrate its counter. A generic closing diagnostic persists after that commit;
it must not expose the detailed panic or prevent global navigation to fresh A.
This example-only failure does not start a newer navigation or change epochs.
