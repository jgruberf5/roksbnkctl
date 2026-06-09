// Package ingressmigration embeds the BNK ingress-migration demo manifests.
// The embedded FS is consumed by the scenario implementation; this file
// ships bytes only.
package ingressmigration

import (
	"embed"
	"io/fs"
)

//go:embed manifests/*.yaml
var manifestFS embed.FS

// ManifestFS returns the embedded ingress-migration demo manifests as an fs.FS.
func ManifestFS() fs.FS { return manifestFS }
