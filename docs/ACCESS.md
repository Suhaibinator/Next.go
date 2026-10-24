# Access results and authorization boundaries

Pages and synchronous server data loaders can return `nextgo.ErrUnauthorized`
(401) or `nextgo.ErrForbidden` (403). Use ordinary Go error returns, including
`fmt.Errorf("...: %w", err)`. A denial discards the failed page and its metadata;
wrapped error messages never enter the denial UI. Panics remain generic server
errors. These results select UI and status; the application verifies identity
and authorization before reading or changing protected data.

```go
func Page(c *nextgo.Context) (nextgo.Page, error) {
    record, err := reports.ReadForPrincipal(c.Context, c.Request)
    if err != nil { // includes ErrUnauthorized / ErrForbidden
        return nextgo.Page{}, err
    }
    if err := c.SetHeader("Cache-Control", "private, no-store"); err != nil {
        return nextgo.Page{}, err
    }
    return nextgo.Page{Body: ReportView(record)}, nil
}
```

A data-access function should authenticate the trusted request/session, check the
principal's access to the requested record, and only then call its repository.
Repeat authorization in APIs and actions. An earlier page render, hidden button,
URL parameter, caller-supplied role header or retained browser layout grants no
permission. `Context.SetStatus` changes a response status; it does not perform
these checks or replace a failed page.

## File conventions

```text
app/
  layout.go
  unauthorized.go            optional global Unauthorized(*Context) (Page,error)
  forbidden.go               optional global Forbidden(*Context) (Page,error)
  reports/
    layout.go
    unauthorized.go          nearest Unauthorized(*Context) (Page,error)
    forbidden.go             nearest Forbidden(*Context) (Page,error)
    param_id/page.go
```

Discovery registers the nearest handler for each kind independently, including
transparent route groups. A nested boundary wraps with layouts/templates strictly
above its directory. It excludes same-directory and deeper wrappers, which may
rely on the denied access. If a layout fails, boundaries below that layout cannot
run. Root files map to `Config.Unauthorized` and `Config.Forbidden`, with no
implicit layout. Without a custom handler the framework renders a useful generic
401 or 403 document. A boundary failure/panic returns a generic 500; it cannot
recursively rerun the failed layout. HTTP endpoints bypass page boundary UI.

Hand-written registrations can use `Route.Access []nextgo.AccessBoundary` in
outermost-to-innermost order. Each entry's `Layouts` is its safe outer wrapper
count. `New` rejects negative, decreasing or out-of-range prefixes, empty handlers
and page access boundaries on API routes. File-based applications normally let
the generator calculate these values.

The server evaluates pages before wrapping their returned body in layouts.
Consequently, a layout-only authentication check cannot prevent a page loader
from running. Protect the data-access function itself. Checks must finish before
returning a streamed page or a deferred `ui.Node`: committed streaming output
cannot be retroactively changed into an HTTP 401/403. Deferred access interrupts
and independent segment recovery remain unfinished.

## Privacy, navigation and mutations

Denial pages keep their actual status for documents, HEAD and navigation JSON.
They force `Cache-Control: private, no-store`, remove public prefetch eligibility,
and include `noindex` in the head and `X-Robots-Tag`. Successful private pages
must also use private/no-store policy. Do not declare an authenticated page as an
anonymous public `CachePolicy`; public loaders deliberately reject staged
response mutations. Authentication is not inferred from an arbitrary header.

A 401 boundary should set the challenge appropriate to its authentication scheme,
for example `c.SetHeader("WWW-Authenticate", "Bearer realm=\"reports\"")`.
The framework cannot choose the application's identity provider or challenge.

Go navigation renders denials in the existing document and updates status, title,
metadata and history. A denial replaces the complete application region and
disposes retained layout islands/effects too, even when layout IDs match. This
prevents an identity change from retaining preceding protected UI. It cannot
revoke information already delivered to an authorized browser.

Action handlers can return `actions.ErrUnauthorized` / `actions.ErrForbidden`;
they share the page errors' identities. JSON receives safe `unauthorized` or
`forbidden` errors with HTTP 401/403. Progressive form submissions receive a
generic error document. Failed actions do not publish returned data, redirects
or invalidation callbacks. The handler must reject access **before mutation**.
Configure `actions.Options.UnauthorizedChallenge` when needed; it is a printable
ASCII response value limited to 8 KiB. Action denial responses are private,
no-store and non-indexable. Successful action results remain no-store. Progressive
return links respect the deployment base and slash policy.

## Demo policy and current limits

`examples/demo/access` is a server-only illustrative opaque-token policy. The
`/restricted` page, `/api/report` and `review-report` action reuse its data-level
authorization check. Unconfigured principals are disabled. Explicit Bearer
credentials map to reader or administrator; readers receive 403 before report
loading or mutation, and only administrators can read/review the report. Query
strings and role headers are ignored. Token comparisons use fixed-size digests
and constant-time equality; tokens are not props, metadata or runtime bundles.

To try successful access, supply `NEXTGO_DEMO_READER_TOKEN` and/or
`NEXTGO_DEMO_ADMIN_TOKEN` to the server from your secret manager. Use distinct,
cryptographically random values (32–512 non-whitespace characters), and send
`Authorization: Bearer <token>` over HTTPS. Neither variable is exposed to browser
components. The test harness uses synthetic credentials in isolated processes.
The example has no public login/account endpoint, sessions, token expiry/rotation,
identity database or durable review log. Its review counter is process-local.
Managed authentication, revocation, distributed sessions and a production identity
provider integration remain separate parity and release work.

The related Next.js [unauthorized](https://nextjs.org/docs/app/api-reference/functions/unauthorized)
and [forbidden](https://nextjs.org/docs/app/api-reference/functions/forbidden)
features are experimental in the current comparison baseline. Next.go's explicit
Go error API and safe layout-prefix convention do not promise source compatibility
or the baseline's after-stream interrupt behavior.
