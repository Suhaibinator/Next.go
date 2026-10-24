# Local fonts and stylesheet links

The `assets` package generates typed head links and a content-addressed local
font stylesheet. It uses no browser JavaScript and does not fetch remote fonts.
Provide font files you are licensed to distribute; the paths below are examples,
not bundled third-party assets.

```go
font := assets.Font{
    Family: "Application Sans",
    Source: "/assets/application-sans.742aa1.woff2",
    Format: "woff2",
    Weight: "100 900",
    Display: "swap",
}

fontHandler, fontCSS, err := assets.NewFontStyles([]assets.Font{font})
if err != nil {
    return err
}
// Register in nextgo.Config.Handlers before creating the application:
config.Handlers[fontCSS] = fontHandler

// Resources contribute escaped <link> elements to the document head:
resources := []assets.Resource{
    assets.Stylesheet(fontCSS),
    font.Preload(),
    assets.Stylesheet("/assets/application.css"),
}
```

Application CSS can then select `font-family: "Application Sans", sans-serif`.
For framework-independent integration, call each resource's `Elements` method,
handle validation errors, and escape the returned `seo.Element` attributes when
rendering. `Page.Resources` accepts this resource list when using the framework.
Initialize `config.Handlers` before assigning to it if it is nil.

`Weight` accepts an integer from 1 through 1000 or an ascending two-number
variable-font range. `Style` accepts `normal`, `italic`, or `oblique`. `Display`
accepts the CSS font-display keywords and defaults to `swap`. Optional
`SizeAdjust`, `AscentOverride`, `DescentOverride`, and `LineGapOverride` pointers
emit numeric percentage overrides on that font face. They are not measured or
inferred automatically. Size adjustment must be positive; all overrides are
bounded to 1000 percent. An explicit zero is supported for metric overrides.

The generated CSS handler supports GET, HEAD, ETag revalidation, and immutable
caching. Its URL hashes the font declarations, including source URLs. When font
file bytes change, give the source a new versioned or content-addressed URL too.
Only preload critical faces; preloading every weight can increase bandwidth and
delay other resources. The browser performs its own font decoding and loading.

Optional stylesheet integrity accepts complete `sha256`, `sha384`, or `sha512`
base64 digests. The helper sets anonymous CORS mode when integrity is supplied.
URL validation allows local root-relative files, excludes remote origins and
path traversal, and safely encodes font-family and source strings into CSS.

This is local font delivery support. Automatic downloading, license discovery,
subsetting, format conversion, glyph-based splitting, fallback-metric calculation,
and third-party script loading are not implemented by this package.
