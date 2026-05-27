// Package diameter embeds the BNK diameter demo manifests and python client
// scripts (proven live 2026-05-27).
// The embedded FSes are consumed by Slices C1/C2 which add the
// scenarios.Scenario implementation; this slice (Embed) ships bytes only.
package diameter

import (
	"embed"
	"io/fs"
)

//go:embed manifests/*.yaml
var manifestFS embed.FS

//go:embed diameter_client.py responder.py
var clientFS embed.FS

// ManifestFS returns the embedded diameter demo manifests as an fs.FS.
func ManifestFS() fs.FS { return manifestFS }

// ClientFS returns the embedded diameter python client scripts as an fs.FS.
func ClientFS() fs.FS { return clientFS }
