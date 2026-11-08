# Matched production comparison fixture

This deliberately small application renders two request-time pages and one
counter. SSR supplies the initial count, 3, and a disabled button; Go/WASM
preserves that output node, enables the button and increments to 4. Ordinary
links work without JavaScript. Automatic prefetch is disabled for measurement.
The readiness mark belongs to this fixture and does not measure complete
framework hydration.

The CSS, text, counter behavior and original RGBA favicon match the isolated
Next.js reference in `tests/performance/nextjs`. That React/Next.js dependency
exists only for comparison; the Go application has no npm dependency or
application JavaScript. There is no explicit route cache or static prerender.

```sh
go run ./cmd/nextgo generate --check ./examples/comparison
CGO_ENABLED=0 go run ./cmd/nextgo build -out ./dist/comparison ./examples/comparison
./dist/comparison/server -addr 127.0.0.1:3000
```

See [comparison controls and limitations](../../docs/FRAMEWORK_COMPARISON.md).
This workload does not cover the full parity inventory or certify performance.
