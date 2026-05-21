// Package manifests exposes the embedded k8s manifest directories as an fs.FS.
// Phases use this FS to read manifest files without requiring them to be on disk
// at runtime — the manifests are baked into the binary at compile time.
//
// Layout:
//
//	cert-manager/   — cert-manager v1.16.1 static install YAML (upstream verbatim)
//	multus/         — Multus CNI v4.2.4 daemonset YAML (upstream verbatim, slice 7+)
//	shared/         — BNK cert chain template (applied to every cluster/pattern)
//	host-device/    — variant manifests for host-device pattern (slice 6+ content)
//	sr-iov-tmm/     — variant manifests for sr-iov-tmm pattern (slice 6+ content)
package manifests

import "embed"

// FS is the embedded manifest filesystem. The "all:" prefix ensures dotfiles
// (e.g. .gitkeep) are included so the scaffold directories are preserved.
//
//go:embed all:cert-manager all:multus all:shared all:host-device all:sr-iov-tmm
var FS embed.FS
