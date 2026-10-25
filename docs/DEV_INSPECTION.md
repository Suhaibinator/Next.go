# Development inspection

While `nextgo dev` is running, read its public build metadata with:

```sh
nextgo inspect -addr localhost:3000
```

The command prints JSON and exits. It connects only to `localhost` or a literal
loopback IP, bypasses DNS and HTTP proxies, refuses redirects, and bounds its
request time and response size. The command does not need an application path.
The HTTP equivalent is `GET http://localhost:3000/_nextgo/inspect`.

The endpoint exists only on the development supervisor. Production application
servers do not register it. It supports `GET` and `HEAD` and has no control or
write operations. This is an experimental version 1 protocol, not an MCP server.

## Local access

The connection peer and request Host must both be loopback addresses or the
`localhost` Host name, and the Host port must match the supervisor listener.
Forwarded headers do not establish local access. An explicitly configured LAN
development bind does not grant LAN inspection access.

Browser requests must have no Origin header or exactly the request's HTTP
origin. Opaque, duplicate, and different origins are rejected. Fetch Metadata
requests marked `cross-site` or `same-site` are rejected. There is no CORS opt-in.
These checks prevent an unrelated browser origin or DNS rebinding host from
reading the graph. Local processes can read the endpoint without a token.
Responses use `Cache-Control: no-store`.

## Snapshot semantics

`version` identifies the schema. `revision` increases for build state changes.
`state` is `building`, `ready`, or `failed`. `buildId` identifies the last
successful build and `generation` identifies its server process, so restarting
the same build with updated environment settings changes the generation.
`serving` reports whether that process is available through the supervisor.

During a rebuild or a failed rebuild, the graph and build identity describe the
last successful build. `stale` is then true. An initial build has an empty graph.
A server exit sets `serving` to false and retains the last graph for inspection.
`problem`, when present, is a fixed code: `build_failed` or `server_exited`.
Compiler error text is available in the existing development diagnostics and is
not included in inspection responses. A successful rebuild clears the problem
and atomically publishes the new graph, build identity, and generation.

The graph contains page and API route patterns, anonymous layout and template nodes, named
slots, and browser component names and lazy flags. Directed edges describe route
layouts and templates, route slots, slot routes, interception routes, and slot defaults. Array
order preserves layout order. Node IDs are opaque and scoped to one graph;
clients should not compare them across builds. Components are an inventory;
the graph does not infer which page uses each browser component. The graph uses
the discovery metadata from the successful build, rather than rediscovering
files that may already have changed.

Source code, source and module paths, import identities, environment values,
configuration values, action data, request headers and bodies, and component
props have no fields in this protocol. Public route patterns and component names
are intentionally included. The graph does not execute application renderers,
configuration hooks, or action handlers.

Each response is at most 1 MiB. Graphs contain at most 2,048 nodes and 4,096 edges;
public names and patterns are limited to 512 UTF-8 bytes. Graph traversal and
nesting are bounded as well. `graph.truncated` indicates omitted entries or
oversized or invalid public metadata. Edges always reference included nodes.
