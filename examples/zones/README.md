# Independent applications and tenant context

`zones.json` declares two independent Go applications on one exact public host:
marketing owns the root and blog owns `/blog`. Each has its own pages, layout,
stylesheet, Go counter, API and native action registration. Both use logical
`/api/status`, `/_nextgo/actions/preview`, `/posts/:slug`, and a cache key/tag with
the same names. Ownership supplies their separate public mounts and local stores.

The counters include compiled Go proof: marketing increments by 1, blog by 10,
and the reused tenant application by 100. Same-application navigation retains a
counter; crossing an application boundary loads the target application's document
and runtime. Public assets are original local CSS. There is no application
JavaScript beyond the WASM bridge.

The same tenant source is instantiated twice for exact hosts
`alpha.example.test` and `beta.example.test`. Trusted `CurrentZone` context picks
the synthetic repository. It is **not** a visitor identity. Each protected page,
record API and preview action independently verifies a demo session and tenant
membership before accessing the record. Alice belongs to alpha; Bob belongs to
beta. A valid Alice identity still receives a denial at beta.

The root tenant page is an explicitly public synthetic issuer: anyone may assume
Alice or Bob. Its process-local HMAC key signs short-lived HttpOnly demo cookies
and is never a browser prop. This shows session integrity and separate membership
checks, not real account authentication. All records are public fixture data;
there are no real credentials, private records or durable writes. Process restart
retires issued sessions and resets preview revisions.

The marketing application configures one managed sitemap at startup, capturing
its trusted `Config.Zone().Host`. It never derives crawler origins from an
incoming Host or forwarded header. Its configured source uses HTTP for this local
example; production applications must choose their trusted public HTTPS origin.

Build/run through the zones CLI once available:

```sh
go run ./cmd/nextgo zones build -out /tmp/nextgo-zones ./examples/zones/zones.json
/tmp/nextgo-zones/server -addr 127.0.0.1:4230
```

The manifest hosts are exact authorities. Local browser fixtures use an explicit
startup host override for alpha/beta on separate loopback ports, preserving real
browser origins; request headers or query strings do not select that override.
Manifest application paths resolve relative to the manifest, not the shell's
working directory. The operator must publish the declared host/path mapping.

This is a bounded in-process zones example, not an arbitrary reverse proxy or a
complete production tenant platform. One static export cannot reproduce the
multi-application host dispatcher; the zones bundle requires its supported server
deployment. Application-specific authorization, storage, trusted-origin policy,
observability, resource budgets and production qualification remain necessary.
