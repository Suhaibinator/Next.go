//go:build !js

package shared

import _ "embed"

//go:embed note.txt
var Note string
