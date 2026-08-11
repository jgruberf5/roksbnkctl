# Per-BNK-line terraform overlays

Everything above this directory is the **base** tree: the HCL that is true for
every supported BNK release. This directory holds the parts that are *not*.

## Why overlays and not a branch

A branch per BNK release was the first answer and it was the wrong one. It forks
the whole tool — CLI fixes, IBM module updates, docs, CI — to express a
difference that lives in a handful of `.tf` files, and it makes every subsequent
change pick a target branch. Overlays keep one trunk and localise the difference
to the files that actually differ.

## How it works

`terraform/lines/<line>/` mirrors the layout of the base tree. At extraction
time the base is written first, then the overlay for the line derived from
`bnk.manifest_version` is written **on top** — same relative paths overwrite,
new paths are added. Nothing is deleted, so an overlay can only add to or
replace the base, never quietly remove a resource out from under it.

The line comes from the manifest version already in config.yaml
(`2.3.0-3.2598.3-0.0.170` → `2.3`), so selecting a layer costs no new config
field and cannot drift from the BNK release actually being installed.

## When to add one

Only when a release genuinely diverges — new resource types, changed module
inputs, an IBM provider version the base cannot use. A difference that a
variable can express belongs in the base as a variable, not here: an overlay is
a fork of a file, and two copies of a file drift.

**An empty `lines/` directory is the correct state** whenever every supported
release can be served by the base tree, which is the case today. The mechanism
exists so that the release which needs it does not also need a re-architecture.
