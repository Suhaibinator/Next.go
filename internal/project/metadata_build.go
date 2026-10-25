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
func writeMetadataSources(b *strings.Builder, p *Project, aliases map[string]string, staticRoot ...string) {
	root := "assets/metadata"
	if len(staticRoot) > 0 {
		root = staticRoot[0]
	}
	if hasStaticMetadata(p) {
		fmt.Fprintf(b, "config.MetadataFiles.Static,err=fs.Sub(embedded,%q);if err!=nil{log.Fatal(err)}\n", root)
	}
	for _, resource := range p.MetadataFiles {
		if !resource.Static() {
			fmt.Fprintf(b, "config.MetadataFiles.%s=%s.%s\n", resource.Factory, aliases[resource.Import], resource.Factory)
		}
	}
}
