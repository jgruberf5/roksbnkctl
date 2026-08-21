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
// Two directives, deliberately.
//
// The first embeds the committed HCL. It is NOT `all:terraform`: the `all:`
// prefix pulls in dotfiles, including the gitignored `.terraform/`
// provider/module cache (~400MB of plugin binaries) that a local `terraform
// init`/`validate` leaves in ./terraform during development. Embedding that
// bloated the binary to ~670MB AND, once extracted (extractEmbeddedTF writes
// 0644), shipped non-executable provider binaries that broke `terraform plan`
// with "fork/exec ... permission denied".
//
// The second names .terraform.lock.hcl explicitly, because a plain directory
// pattern skips every dotfile and this one has to ship (#147). It pins each
// provider to an exact version and records the checksums `terraform init`
// verifies downloads against. Without it in the binary, `init` runs in the
// extract directory against the `>=` constraints in versions.tf alone, so two
// operators running the same release on different days can resolve different
// providers, and nothing verifies what the registry serves. CI ran against the
// committed lockfile while every user got a freshly resolved set.
//
// An explicit path is what keeps this from costing anything: it ships the one
// dotfile that matters without reopening the `all:` problem above.
//
//go:embed terraform
//go:embed terraform/.terraform.lock.hcl
var EmbeddedTerraform embed.FS
