//go:build !js

// Package settings owns the example's immutable native startup selection.
package settings

var component = "workspace"

// Configure is called only by the generated application's Configure hook,
// before requests. Invalid operator input fails startup instead of publishing a
// declaration that disagrees with the selected ClientIsland.
func Configure(mode string) {
	switch mode {
	case "", "eager":
		component = "workspace"
	case "lazy":
		component = "workspacelazy"
	default:
		panic("NEXTGO_STATIC_SHELL_MODE must be eager or lazy")
	}
}

func Component() string { return component }
