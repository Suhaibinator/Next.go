# alpha module

This application is a separate Go module. It imports only the framework and its
own packages; it does not import its peer application. See the topology README
two directories above for independent release and ingress operation.

All data is public and process-local. `NativeVersion` in `internal/state/state.go`
and the compiled lazy counter constants are explicit fixture edit points.
