// Package accesscontrol shares denial results without importing the server
// framework into browser-visible action response types.
package accesscontrol

import "errors"

var Unauthorized = errors.New("nextgo: unauthorized")
var Forbidden = errors.New("nextgo: forbidden")
