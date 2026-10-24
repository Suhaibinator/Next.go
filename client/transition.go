package client

import "fmt"

// TransitionOptions controls a progressive browser view transition. Disabled
// updates immediately. Types are optional CSS transition names (at most eight
// ASCII identifiers of 1..63 bytes); older browsers may ignore them.
type TransitionOptions struct {
	Disabled bool
	Types    []string
}

// NavigationOptions applies to NavigateWithOptions. Transition options customize
// navigation only when the application's Config.ViewTransitions is enabled.
type NavigationOptions struct {
	Replace    bool
	Transition TransitionOptions
}

func (o TransitionOptions) validate() error {
	if len(o.Types) > 8 {
		return fmt.Errorf("nextgo: at most eight transition types are allowed")
	}
	for _, name := range o.Types {
		if len(name) == 0 || len(name) > 63 || name == "none" || name == "auto" {
			return fmt.Errorf("nextgo: invalid transition type")
		}
		for i, c := range name {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && (c >= '0' && c <= '9' || c == '_' || c == '-')) {
				return fmt.Errorf("nextgo: invalid transition type")
			}
		}
	}
	return nil
}
