#!/bin/sh
set -eu

# Run Go browser-client tests using Node's standard Fetch/Response/stream APIs.
# Keep the runner and test compiler from the same Go installation.
task_goroot=$(go env GOROOT)
GOOS=js GOARCH=wasm go test -exec="$task_goroot/lib/wasm/go_js_wasm_exec" ./client ./mutation ./offline "$@"
# The private component target uses compile-time branches. Exercise its public
# document-API forwarding separately from the core router's fixture tests.
GOOS=js GOARCH=wasm go test -tags=nextgo_module -exec="$task_goroot/lib/wasm/go_js_wasm_exec" ./client \
  -run '^(TestModuleTarget|TestRefresh|TestFormFieldValidation|TestFormValidationStatus|TestFieldValidation|TestOptionalChoice)'
# Private development entries retain the complete refresh/state/drain contract.
# Production tests above run without this tag and must remain independently green.
GOOS=js GOARCH=wasm go test -tags=nextgo_devrefresh -exec="$task_goroot/lib/wasm/go_js_wasm_exec" ./client ./mutation "$@"
GOOS=js GOARCH=wasm go test -tags='nextgo_module nextgo_devrefresh' -exec="$task_goroot/lib/wasm/go_js_wasm_exec" ./client \
  -run '^(TestModuleTarget|TestRefresh|TestFormFieldValidation|TestFormValidationStatus|TestFieldValidation|TestOptionalChoice)'
