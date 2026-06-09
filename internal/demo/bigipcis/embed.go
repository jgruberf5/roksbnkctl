// Package bigipcis embeds the bigip-cis demo manifests. The embedded FS is
// consumed by the scenario implementation; this file ships bytes only.
package bigipcis

import (
	"embed"
	"io/fs"
)

//go:embed manifests/*.yaml
var manifestFS embed.FS

// ManifestFS returns the embedded bigip-cis demo manifests as an fs.FS.
func ManifestFS() fs.FS { return manifestFS }
