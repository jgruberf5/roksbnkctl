// Package embedded ships the agentic-mode scaffolding inside the binary.
//
// files/ is copied into a workspace by `roksbnkctl agent init`: AGENTS.md
// (the shared operator reference), CLAUDE.md (a one-line @AGENTS.md include),
// personas/ (the role contracts), and decisions.md (the decision-log seed).
//
// This is distinct from the module-root //go:embed of ./terraform (the HCL
// source tree) — that one lives at the module root because embed paths are
// resolved relative to the embedding file's directory.
package embedded

import "embed"

// FS holds everything under files/. Note: //go:embed skips dotfiles, so the
// journal/.gitkeep placeholder is NOT embedded — `agent init` MkdirAll's the
// journal/ dir itself.
//
//go:embed files
var FS embed.FS
