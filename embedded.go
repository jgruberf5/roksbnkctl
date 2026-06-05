// Package roksbnkctl exposes the embedded Terraform source tree as an
// embed.FS so the compiled binary ships with the matched HCL — no
// separate TF download for the default deploy path. External tf_source
// overrides (a GitHub release, a local path) bypass this and still
// work for users who want to test forks.
//
// This file lives at the module root because //go:embed paths are
// resolved relative to the source file's directory and Go forbids
// embedding paths outside the embedding file's package — so the
// embedding shim has to sit alongside ./terraform/.
package roksbnkctl

import "embed"

// EmbeddedTerraform is the entire ./terraform/ tree (HCL root + modules
// + terraform.tfvars.example). Walked at runtime by tf.FetchSource when
// the workspace's tf_source is unset / type=embedded, and extracted
// into the workspace state dir for terraform-exec to operate on.
//
// NOTE: deliberately NOT `all:terraform`. The `all:` prefix pulls in
// dotfiles — including the gitignored `.terraform/` provider/module cache
// (~400MB of plugin binaries) that a local `terraform init`/`validate`
// leaves in ./terraform during development. Embedding that bloated the
// binary to ~670MB AND, once extracted (extractEmbeddedTF writes 0644),
// shipped non-executable provider binaries that broke `terraform plan`
// with "fork/exec ... permission denied". Plain `terraform` embeds the
// committed HCL source and skips every dotfile, so `.terraform/` is never
// bundled regardless of what a dev machine has on disk; terraform init
// resolves + pins providers from the version constraints in the .tf files
// at deploy time. (The .terraform.lock.hcl files are not committed, so
// `all:` only ever embedded them opportunistically from a dev machine —
// nothing reproducible is lost here.)
//
//go:embed terraform
var EmbeddedTerraform embed.FS
