// Package http2 embeds the BNK http2 demo manifests (proven live 2026-05-27).
// The embedded FS is consumed by Slices C1/C2 which add the scenarios.Scenario
// implementation; this slice (Embed) ships bytes only.
package http2

import (
	"embed"
	"io/fs"
)

//go:embed manifests/*.yaml
var manifestFS embed.FS

// ManifestFS returns the embedded http2 demo manifests as an fs.FS.
func ManifestFS() fs.FS { return manifestFS }
