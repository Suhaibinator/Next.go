# My Go application

Requires Go 1.24 or newer. This starter uses a local replacement for the experimental
Next.go framework; keep that source checkout available while developing/building.

```sh
go run github.com/Suhaibinator/Next.go/cmd/nextgo dev .
go run github.com/Suhaibinator/Next.go/cmd/nextgo build -out ./dist .
./dist/server -addr :3000
```

Open the address printed by development. No Node installation is required.
Production output embeds its public assets, Go/WASM and matching runtime support.
The server binary runs independently of both source checkouts.

- `app/page.go` renders the home page and counter.
- `app/layout.go` is the shared layout; `app/notfound.go` supplies the custom 404.
- `app/about/page.go` demonstrates client navigation.
- `app/hello/param_name/page.go` reads a decoded dynamic parameter.
- `app/api/health/route.go` is a standard HTTP handler.
- `components/counter` separates shared props, server HTML and browser behavior.
- `public/style.css` is served at `/assets/style.css`.

Browser props are public data. Use `serveronly` in packages holding server services
and `clientonly` in browser interop packages. Keep server component files guarded
with `//go:build !js`; client files use `//go:build js && wasm`.
