package nextgo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/Suhaibinator/Next.go/scripts"
	"html"
	"net/http"
	"strings"
)

func freezeEarlyScripts(input []scripts.Script) ([]scripts.Script, error) {
	if len(input) > 16 {
		return nil, fmt.Errorf("scripts: at most sixteen early declarations are supported")
	}
	output := make([]scripts.Script, len(input))
	ids, sources := map[string]bool{}, map[string]bool{}
	for i, script := range input {
		s, err := script.Check()
		if err != nil {
			return nil, err
		}
		if s.Strategy != scripts.BeforeInteractive {
			return nil, fmt.Errorf("scripts: Config.EarlyScripts requires beforeInteractive")
		}
		if ids[s.ID] || sources[s.Src] {
			return nil, fmt.Errorf("scripts: duplicate early ID or source")
		}
		ids[s.ID], sources[s.Src] = true, true
		output[i] = s
	}
	return output, nil
}
func earlyScriptsIdentity(input []scripts.Script) string {
	if len(input) == 0 {
		return ""
	}
	data, _ := json.Marshal(input)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func (a *App) earlyScripts(r *http.Request) string {
	markup, err := scripts.EarlyHTML(a.config.EarlyScripts, a.paths.AssetURL, scripts.Nonce(r.Context()))
	if err != nil {
		panic(err)
	} // Both configuration and asset paths were checked by New.
	return markup
}

func nonceHead(head, nonce string) string {
	if nonce == "" {
		return head
	}
	return strings.ReplaceAll(head, "<script ", `<script nonce="`+html.EscapeString(nonce)+`" `)
}
func nonceResponse(w http.ResponseWriter, r *http.Request) {
	if scripts.Nonce(r.Context()) != "" {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set(scripts.NonceHeader, "1")
		clearNavigationReuse(w.Header())
	}
}
