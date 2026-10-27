# Serverless request correlation context

**Experimental — runtime qualification pending.** The two getters below are
implemented and source-reviewed. Their fresh unit and generated native/local
buffered/local streaming executable checks have not run, and public CI is
pending. No AWS, browser or production qualification follows from publication.

## Read two distinct identifiers

The API exposes these functions in `github.com/Suhaibinator/Next.go/serverless`:

```go
func InvocationID(context.Context) string
func GatewayRequestID(context.Context) string
```

`InvocationID` reads the Runtime API invocation ID attached by `serverless.Run`.
`GatewayRequestID` reads the HTTP API v2 event's
`requestContext.requestId`, attached by Run or `Request.HTTPRequest`. They can
be different values. They are correlation metadata, not credentials, an
authorization decision, an authenticated user or a tracing identity.

Both getters return `""` for nil context or absent metadata. Accepted values
are independently owned string copies, at most 256 bytes, with every byte in
`0x21..0x7e` (visible ASCII excluding space). Missing, overlong, non-ASCII or
otherwise unsuitable metadata is omitted as `""`; it is never truncated or
normalized into another identifier.

This metadata rule is separate from Runtime API transport validation. Existing
invocation-ID validation and response/error routing remain unchanged. An ID
accepted by the existing transport can still be unsuitable for these bounded
getters: dispatch and transport routing continue, while metadata is empty.
This does not tighten that transport contract.

Ordinary HTTP headers such as `X-Request-ID` or
`Lambda-Runtime-Aws-Request-Id` do not supply or override either getter. The
adapter reads its Runtime API envelope/header and event request context at the
owned boundaries; changing a converted request's header map cannot change its
context metadata. Ordinary headers are still application input and are not
silently removed by this feature.

## Application-owned handler and completion logging

For example, an ordinary `net/http` handler registered inside a Next.go App can
choose to log the values and register bounded completion work:

```go
package handlers

import (
    "context"
    "log"
    "net/http"

    nextgo "github.com/Suhaibinator/Next.go"
    "github.com/Suhaibinator/Next.go/serverless"
)

func Check(w http.ResponseWriter, r *http.Request) {
    log.Printf("check invocation=%q gateway=%q",
        serverless.InvocationID(r.Context()),
        serverless.GatewayRequestID(r.Context()))

    err := nextgo.After(r.Context(), func(work context.Context) error {
        log.Printf("check completed invocation=%q gateway=%q",
            serverless.InvocationID(work),
            serverless.GatewayRequestID(work))
        return nil
    })
    if err != nil {
        http.Error(w, "completion unavailable", http.StatusServiceUnavailable)
        return
    }
    w.WriteHeader(http.StatusNoContent)
}
```

This logging is an application opt-in illustrated by the example. The framework
does not add response headers, log fields, telemetry attributes, environment
variables or X-Ray/W3C trace conversion for these IDs. Choose your own log
format and retention policy; do not use a correlation value as an access token.

`After` must be registered while a framework completion scope is active. A raw
standalone handler context or direct adapter call need not provide that scope;
handle the returned error. Existing completion rules remain unchanged: request
values survive in the bounded post-response context, client cancellation does
not erase them, and Lambda Run starts callbacks only after accepted response
publication and drains them before the next event. Failed publication discards
callbacks. Do not retain a ResponseWriter, request body or mutable request state
in the callback. This feature does not add durable work, retries or new scheduling.

## Native, direct-adapter and warm-context behavior

| Entry point | Invocation metadata | Gateway metadata |
| --- | --- | --- |
| Ordinary native HTTP with an ordinary request context | Empty | Empty |
| `serverless.Run`, buffered or streaming | This invocation's bounded Runtime API ID | This event's bounded `requestContext.requestId` |
| `serverless.Adapt` or `event.HTTPRequest(context.Background())` without Run | Empty | This event's bounded gateway ID |
| `event.HTTPRequest(parent)` with inherited invocation metadata | Preserved from the supplied parent | Replaced by this event, including empty omission |

Every Run dispatch attaches fresh values for that event. HTTPRequest replaces
gateway metadata after constructing the request, even when the new event omits
its ID or supplies a nonportable value. That empty value shadows inherited
metadata instead of accidentally borrowing a previous gateway request's ID.
It does not invent a Runtime API ID for direct adapter use.

A retained old context stays an immutable snapshot of its old values, even
after a later warm invocation. Cancellation/deadline state remains its own;
reading a getter does not renew or detach the deadline. Do not keep an old
context as a process-global way to discover the current invocation. Explicitly
passing a captured parent to HTTPRequest retains that parent's invocation ID;
this is context inheritance, not automatic native request identity.

## Proof boundary

The API unit tests and generated-executable fixture are prepared to check
omission/ownership, spoofed headers, warm reuse, cancellation, completion and
native/local buffered/local streaming behavior. They remain unexecuted for this
feature. Existing deployment checks keep their own checkpoint scope.

The [dedicated workflow](../.github/workflows/serverless-context.yml) declares
four build/run phases per SDK: build the real CLI once, build the file-routed
fixture once, compile the separately tagged test and run the actual executable
checks. Each future run requires three fresh native children (HTTP, buffered
mock Runtime API and streaming mock Runtime API), with three requests per
child to exercise warm reuse. These are future assertions, not observed passes.
The workflow targets Linux AMD64 nonroot functional behavior, not read-only
Docker isolation, ARM64, whole-process resource/soak limits or final namespace
sampling.

The fixture requires **both** `deployment` and `invocation_context` tags. Its
compile command selects them together:

```sh
go test -c -tags=deployment,invocation_context -o /absolute/work/invocation.test ./tests/deployment
```

Use the [fixture build/run instructions and proof contract](../tests/deployment/fixtures/invocation-context/README.md)
for the complete four-command sequence, absolute prebuilt binary, actual SHA-256
and UID inputs, external working directory and output verifier. The test has no
application-build fallback; missing inputs fail rather than skip. Plain
`go test -tags=deployment ./tests/deployment` excludes this fixture and retains
its existing demo-binary interface. No Node/Python or browser is needed by this
fixture.

Adding CI does not prove real Runtime API delivery, AWS IAM, Function URL/API
Gateway deployment, X-Ray integration or full serverless parity. All 79
capability IDs and 11 open release gates remain.
