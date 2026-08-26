package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/registry/mirror"
)

// runRegistryAdopt records an existing mirror without replicating into it.
//
// The record it writes is identical to replicate's in every field the rest of the
// tool reads, with one deliberate exception: Artifacts is empty unless
// --verify-contents was passed, because without a BOM there is no way to know what
// the mirror holds. That matters for `registry delete`, which walks Artifacts — an
// adopted record cannot drive a delete, and adopt says so rather than leaving a
// later delete to silently remove nothing.
func runRegistryAdopt(cmd *cobra.Command, _ []string) error {
	name, ws, err := loadRegistryWorkspace()
	if err != nil {
		return err
	}
	target, err := buildTarget(cmdContext(cmd), name, ws)
	if err != nil {
		return err
	}
	in := resolveBOMInputs(ws)

	// The CA is resolved the same way replicate resolves it — config/file first,
	// else an out-of-band-PINNED capture. Adoption does not relax that: an
	// unpinned capture is refused here exactly as it is there, because the CA
	// ends up in every node's trust store either way.
	pushHost := registryHostFromPath(target.ImageHostPath())
	mirrorCA, caErr := resolveMirrorCA(name, ws, pushHost)
	if caErr != nil {
		return caErr
	}

	// The engine must TRUST that CA, not merely record it: both things adopt does
	// over the network — ProbeNamespace's catalog listing and --verify-contents'
	// digest checks — go through crane. Without it a self-signed mirror (the entire
	// target case for adoption) fails x509: the probe degrades to a warning and
	// silently loses its only validation, and --verify-contents reports every
	// artifact as missing.
	// THE ENGINE IS BUILT AFTER THE BOM, NOT BEFORE (#224).
	//
	// registryEngine copies source.SourceAuth(in.FARRepoURL, in.SourceSAB64) at
	// CONSTRUCTION, and buildBOM is what fills in.SourceSAB64 — it resolves the
	// FAR service account from COS when the workspace does not set
	// registry.source_service_account_b64. Constructing first captured an EMPTY
	// credential, so every source whose host is repo.f5.com resolved
	// unauthenticated:
	//
	//	✗ charts/coremond: resolve source: ... DENIED: Unauthenticated request
	//	adopt --verify-contents: 87 of 94 artifacts are missing or digest-mismatched
	//
	// The 7 that passed were the non-F5 dependencies, whose sources are public —
	// which is exactly the shape that makes this look like a mirror problem
	// rather than a credential one. `replicate` and `verify` never hit it
	// because they build the BOM first.
	var eng *mirror.Engine

	var artifacts []config.MirrorArtifact
	manifestVersion := in.ManifestVersion

	if registryAdoptFlags.verifyContents {
		bom, berr := buildBOM(cmdContext(cmd), name, ws, &in, registryScratchDir(name))
		if berr != nil {
			return fmt.Errorf("--verify-contents needs the FAR source to build the BOM: %w", berr)
		}
		// `in` now carries the resolved FAR service account.
		eng = registryEngine(target, in, mirrorCA)
		manifestVersion = bom.ManifestVersion
		// VerifyAll, not Verify: it returns every artifact with its resolved TARGET
		// digest, so the recorded inventory can carry digests. An inventory without
		// them drives a tag-based `registry delete` rather than the digest-based
		// form, which is the reliable one for a registry manifest DELETE.
		results := eng.VerifyAll(cmdContext(cmd), bom)
		var bad []mirror.Result
		for _, r := range results {
			if r.Err != nil {
				bad = append(bad, r)
			}
		}
		if len(bad) > 0 {
			for _, r := range bad {
				fmt.Fprintf(os.Stderr, "  ✗ %s: %v\n", r.Artifact.Name, r.Err)
			}
			return fmt.Errorf("adopt --verify-contents: %d of %d artifacts are missing or digest-mismatched",
				len(bad), len(bom.Artifacts))
		}
		for _, r := range results {
			artifacts = append(artifacts, config.MirrorArtifact{
				Kind: string(r.Artifact.Kind), Name: r.Artifact.Name, Tag: r.Artifact.Tag, Digest: r.Digest,
			})
		}
		fmt.Fprintf(os.Stderr, "✓ verified %d artifacts against the source\n", len(bom.Artifacts))
	} else {
		// The probe touches only the TARGET, so the empty source credential is
		// harmless here — but the engine is still built in-branch so there is
		// exactly one construction site per path and no ordering to re-break.
		eng = registryEngine(target, in, mirrorCA)
		// Source-free sanity check: does the mirror hold anything under the prefix?
		n, perr := eng.ProbeNamespace(cmdContext(cmd), target.MirrorNamespace())
		switch {
		case perr != nil:
			// Not every registry exposes _catalog. Being unable to look is not the
			// same as looking and finding nothing, so this warns rather than fails.
			fmt.Fprintf(os.Stderr, "  ⚠ could not list %s to sanity-check the mirror: %v\n", pushHost, perr)
		case n == 0 && !registryAdoptFlags.force:
			return fmt.Errorf("the mirror at %s holds no repositories under %q — "+
				"check registry.generic_repo_prefix, or pass --force to record it anyway",
				pushHost, target.MirrorNamespace())
		case n == 0:
			fmt.Fprintf(os.Stderr, "  ⚠ %s holds no repositories under %q — recording anyway (--force)\n",
				pushHost, target.MirrorNamespace())
		default:
			suffix := "ies"
			if n == 1 {
				suffix = "y"
			}
			fmt.Fprintf(os.Stderr, "  ✓ %s holds %d repositor%s under %q\n",
				pushHost, n, suffix, target.MirrorNamespace())
		}
	}

	rec := &config.RegistryMirror{
		Target:          registryTargetKind(ws),
		Namespace:       target.MirrorNamespace(),
		ChartHost:       target.ChartHostPath(),
		ImageHost:       target.ImageHostPath(),
		ManifestVersion: manifestVersion,
		Artifacts:       artifacts,
		RegistryHost:    pushHost,
	}
	if mirrorCA != "" {
		rec.CACert = mirrorCA
		fmt.Fprintf(os.Stderr, "  ✓ recorded the mirror CA from %s (nodes install it before pulling)\n", pushHost)
	} else if pushHost != "" {
		fmt.Fprintf(os.Stderr, "  ⚠ no CA recorded for %s — if it is a self-signed mirror, re-run with --registry-ca <file>\n", pushHost)
	}
	if err := config.WriteRegistryMirror(name, rec); err != nil {
		return fmt.Errorf("recording mirror: %w", err)
	}

	fmt.Fprintf(os.Stderr, "✓ adopted the mirror at %s — `bnk up` will render against it\n", target.ChartHostPath())
	if len(artifacts) == 0 {
		fmt.Fprintln(os.Stderr, "  note: no artifact inventory was recorded, so `registry delete` has nothing to "+
			"remove for this workspace. Re-run with --verify-contents (needs the FAR source) to record one.")
	} else {
		fmt.Fprintf(os.Stderr, "  ✓ recorded %d artifacts with digests — `registry delete` can drive from this record\n", len(artifacts))
	}
	return nil
}

// ── verify ──────────────────────────────────────────────────────────────────
