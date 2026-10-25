# Native configuration phases

`Config.Phase` identifies why a native application process is starting. Generated
entrypoints select it before invoking the existing `Configure(*nextgo.Config)`
hook. It is a typed startup contract, separate from request data, environment
file mode, browser runtime settings, and authorization.

| Constant | Purpose |
| --- | --- |
| `PhaseDevelopment` | Development application serving. |
| `PhaseProduction` | Ordinary production HTTP or Lambda serving. |
| `PhasePrerender` | The opt-in build probe capturing private prerender snapshots. |
| `PhaseStaticExport` | Static publication configuration, parameter enumeration, and the temporary export-serving process. |

For example, a stable CSS filename can have a native cache policy while its
static host supplies its own response headers:

```go
func Configure(config *nextgo.Config) {
    if config.Phase != nextgo.PhaseStaticExport {
        config.HeaderRules = append(config.HeaderRules, nextgo.HeaderRule{
            Source: "/assets/style.css",
            Headers: []nextgo.HeaderValue{{
                Name: "Cache-Control", Value: "no-cache, must-revalidate",
            }},
        })
    }
}
```

`nextgo export` selects the export phase automatically; no application-specific
environment switch is needed. It does not translate header rules into static
host configuration. Rules retained by an application still cause static export
to refuse, as do the other existing unsupported server dependencies. Configure
the host/CDN's response policy separately or publish content-hashed assets.

## Startup and compatibility

A normal compile-only `nextgo build` does not execute `Configure`. A build with
prerender declarations executes a native probe in `PhasePrerender`. Its final
binary later starts in `PhaseProduction`, or in `PhaseStaticExport` when used
by the exporter. Development binaries select `PhaseDevelopment`.

Generated `-export-config`, `-export-check`, and `-static-paths` probes all select
`PhaseStaticExport`. The exporter also passes the explicit internal
`-static-export` flag to its temporary serving child. Combining an export
purpose with `-prerender-out` is rejected before application configuration runs.
These are native process controls; a URL, header, public setting, or ambient
environment value cannot select the phase.

Existing `Configure` signatures are unchanged. `New` and `NewContext` normalize
an unset phase using the existing native mode fields: `PrerenderBuildMode`
selects prerender, `Dev` selects development, and otherwise the default is
production. They reject unknown phases, simultaneous development/prerender
modes, and explicit phase/mode conflicts before allocating application
resources. An explicit development phase requires `Dev: true`; an explicit
prerender phase requires `PrerenderBuildMode: true`. Production and static export
require both flags to be false. Phase selection does not silently change those
existing mode controls.

A custom server that invokes its own configuration hook should provide the
phase before that call:

```go
config := nextgo.Config{Phase: nextgo.PhaseProduction}
Configure(&config)
app, err := nextgo.New(config)
```

Construction does not call application `Configure` automatically. Environment
file loading keeps its existing explicit development/production mode and
precedence; setting an environment file mode does not change `Config.Phase`.
Do not use phase as proof of user identity or permission.

## Private snapshots and cache ownership

Production and prerender retain compatible snapshot shape identities. Prerender
is a capture lifecycle, so pages, layouts and metadata intended for production
must retain the same public meaning; changing output just because it is being
captured is not a supported way to generate a second application variant.

Static export has a separate opaque configuration/cache identity. Export startup
does not open production `PrerenderFS`, import its rendered HTML, bootstrap local
snapshots, or consume shared prerender seed tickets. It enumerates known paths
with the existing bounds and context, preserving `FallbackNotFound`, and renders
fresh output under the application's export configuration. Existing prerender
policies, partial-region restrictions and static-publication refusal rules remain
in force. Shared cache bindings include the separate configuration identity, so
export output cannot overwrite production's rendered snapshot keys.

The framework does not publish a phase field into document runtime settings or
the browser manifest. Its opaque ownership identity may differ for export.
Applications remain responsible for their own data-cache keys and isolation
when phase-specific configuration reads or writes external application data.

The focused fixtures exercise defaults/validation, generated native probes,
export child startup, local/shared snapshot isolation, unconsumed production
seed tickets, fallback membership, and the CSS example. This is a bounded native
phase contract, not complete build-configuration or release qualification.
