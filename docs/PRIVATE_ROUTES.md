# Private app folders and literal route names

Within `app/`, a directory beginning with `_` or `private_` opts its complete
subtree out of file routing. Its `page.go`, `route.go`, layouts, templates,
metadata/load/error/access providers, static parameters, prerender policies,
regions, defaults, slots and interceptions do not create registrations or URL
constructors. Discovery does not parse routing declarations inside that subtree.
The rule is applied to both primary and named-slot discovery.

Private routing is independent of Go imports, build inputs and authorization.
A public native page can deliberately import a helper from such a package.
Changes to private Go or embedded source remain part of generation fingerprints
and watch inputs; private directories are not globally ignored source trees.
Use `//go:build !js` for native-only data/code. Importing a package from a browser
component can still include that code in its Go/WASM bundle; a folder name alone
is not a client/server security boundary. Configured HTTP handlers/public assets
and explicit manual route registrations keep their existing ownership rules.

```text
app/
  private_helpers/
    data.go        # Go helper; no file routes anywhere in this subtree
    page.go        # also ignored as a routing declaration
    slot_hidden/   # does not create a slot
  _native/
    data.go        # importable when explicitly referenced
  routing-private/
    page.go        # ordinary public page may import these packages
  literal__public/
    page.go        # intentional public URL /_public
```

`literal_` removes one physical directory prefix and treats the remainder as a
literal URL segment. Examples:

| Directory | URL segment |
| --- | --- |
| `literal__public` | `_public` |
| `literal_private_notes` | `private_notes` |
| `literal_param_id` | `param_id` |
| `literal_group_shop` | `group_shop` |

An empty `literal_` name is invalid. Ordinary URL segment validation and route
collision checks still apply. Private-directory checks use physical names
before literal interpretation, so an escaped underscore is public while an
actual `_public` directory remains private. This reserves the `_`, `private_`
and `literal_` naming conventions; rename an existing public physical directory
with `literal_` if its spelling would now be structural. Go import paths must
also be valid for the actual physical directory.

The `/routing-private` demo deliberately renders public strings from both kinds
of native helper, whose convention-shaped exports have non-routing signatures.
It links to `/_public` through `literal__public`. Hidden source paths remain
unregistered; an application's explicit catch-all or custom handler can still
choose to serve a matching URL, so the convention is not an access-control rule.

The [qualification record](benchmarks/private-routes-qualification.json) pins the
private-folder/literal-route source snapshot and both SDK artifacts. Go 1.24.13
and 1.27.3 each passed 80 native race-tested packages and vet, 12 Chromium cases
across HTTP, buffered Lambda, streaming Lambda and static hosting, 42 compiled
deployment cases on the host and again in a restricted Linux container, plus
34 existing client-only/font Chromium regression cases. The record retains a
concurrent browser port collision and the successful serial rerun. The fixed CI
matrix defines 48 private-route cases across 16 profiles per SDK; other browser
engines and real hosted deployment were not qualified by this local checkpoint.

The private-folder checkpoint predates the separately implemented
[independent root-layout boundaries](ROOT_LAYOUTS.md). Broader naming/migration
policy and full release qualification remain tracked in `docs/PARITY.md`.
