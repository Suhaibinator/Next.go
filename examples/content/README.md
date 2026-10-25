# Bounded Markdown example

From the repository root, using the same Go SDK/framework checkout as the CLI:

```sh
go run ./cmd/nextgo dev ./examples/content
go run ./cmd/nextgo build -out ./dist/content ./examples/content
./dist/content/server -addr :3000
```

`app/guide.md` is a native `go:embed` input. The page parses it with explicit
bounded defaults and returns useful HTML without browser JavaScript. An ordinary
Go component is composed around the parsed node; document text cannot invoke it.
Edit the embedded Markdown to change the content. The normal build-input system
tracks declared Go embed inputs alongside Go sources.

This example has no runtime API dependency and can also be exported:

```sh
go run ./cmd/nextgo export -out ./dist/content-static ./examples/content
```

Serve that output directory with an ordinary static HTTP server. No content
compiler, source checkout or Go SDK belongs on the production static host.
See [the syntax and resource contract](../../docs/CONTENT.md). This deliberately
limited format is not CommonMark, MDX or Next.js source compatibility.
