# Multi-module application

This workspace contains an independent application (`site`) and a sibling shared module (`shared`). Its `go.work` also selects the repository's framework module. The page and the Go/WASM counter import `workspace.example/shared`; its embedded text repository has a native-only build constraint.

From the repository root:

```sh
go run ./cmd/nextgo dev -addr 127.0.0.1:3001 ./examples/workspace/site
# Edit shared/value.go or shared/note.txt and observe a rebuild/reload.
go run ./cmd/nextgo build -out ./dist/workspace ./examples/workspace/site
./dist/workspace/server -addr :3001
```

The compiled server works without this workspace, source files or a Go installation. The same binary supports the framework's Lambda runtime adapter. Workspace dependency resolution happens during compilation, not deployment.

The checked-in `go.work` deliberately uses a local framework checkout. A standalone release should pin a published framework version when available, or supply a controlled local replacement. `GOWORK=off` disables workspace selection and requires independently resolvable `go.mod` dependencies. See [workspace and build-input behavior](../../docs/WORKSPACES.md).
