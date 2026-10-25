package project

import (
	"fmt"
	"strings"
)

func hasStaticMetadata(p *Project) bool {
	for _, resource := range p.MetadataFiles {
		if resource.Static() {
			return true
		}
	}
	return false
}

// Assigning factories to Sources makes the Go compiler check their signatures.
// Only the static subtree is embedded here; no producer runs during discovery.
func writeMetadataSources(b *strings.Builder, p *Project, aliases map[string]string) {
	if hasStaticMetadata(p) {
		b.WriteString("config.MetadataFiles.Static,err=fs.Sub(embedded,\"assets/metadata\");if err!=nil{log.Fatal(err)}\n")
	}
	for _, resource := range p.MetadataFiles {
		if !resource.Static() {
			fmt.Fprintf(b, "config.MetadataFiles.%s=%s.%s\n", resource.Factory, aliases[resource.Import], resource.Factory)
		}
	}
}
