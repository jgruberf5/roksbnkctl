// Package roksctl exposes the embedded Terraform source tree as an
// embed.FS so the compiled binary ships with the matched HCL — no
// separate TF download for the default deploy path. External tf_source
// overrides (a GitHub release, a local path) bypass this and still
// work for users who want to test forks.
//
// This file lives at the module root because //go:embed paths are
// resolved relative to the source file's directory and Go forbids
// embedding paths outside the embedding file's package — so the
// embedding shim has to sit alongside ./terraform/.
package roksctl

import "embed"

// EmbeddedTerraform is the entire ./terraform/ tree (HCL root + modules
// + terraform.tfvars.example). Walked at runtime by tf.FetchSource when
// the workspace's tf_source is unset / type=embedded, and extracted
// into the workspace state dir for terraform-exec to operate on.
//
// `all:` prefix includes dotfiles too — keeps any future
// .terraform.lock.hcl in the bundle so provider versions stay pinned
// to what we tested.
//
//go:embed all:terraform
var EmbeddedTerraform embed.FS
