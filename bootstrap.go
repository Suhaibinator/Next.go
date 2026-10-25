package nextgo

import "html"

// The parser must encounter the completed public shell before running either
// runtime script. Classic scripts preserve SDK-before-bootstrap execution while
// the bootstrap's asynchronous WASM work leaves subsequent HTML free to parse.
func runtimeScripts(base, nonce string) string {
	attribute := ""
	if nonce != "" {
		attribute = ` nonce="` + html.EscapeString(nonce) + `"`
	}
	source := html.EscapeString(base)
	return `<script` + attribute + ` src="` + source + `wasm_exec.js"></script><script data-nextgo-bootstrap=""` + attribute + ` src="` + source + `boot.js"></script>`
}
