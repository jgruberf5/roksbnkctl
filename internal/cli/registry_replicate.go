package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

func runRegistryReplicate(cmd *cobra.Command, _ []string) error {
	name, ws, err := loadRegistryWorkspace()
	if err != nil {
		return err
	}
	in := resolveBOMInputs(ws)
	bom, err := buildBOM(cmdContext(cmd), name, ws, &in, registryScratchDir(name))
	if err != nil {
		return err
	}
	target, err := buildTarget(cmdContext(cmd), name, ws)
	if err != nil {
		return err
	}
	// Resolve the mirror's CA up front (before any copy): the file/config copy wins,
	// else an OUT-OF-BAND-PINNED capture from the push host (refused when unpinned).
	// A private/self-signed mirror (co-located Harbor by private IP) returns its CA
	// here; a public target returns "". When
	// set, the engine trusts it for the push TLS so a container operator with no OS
	// trust for the mirror can still replicate — and it is also recorded below for
	// air-gap node trust, so the same CA drives both the operator and the nodes.
	pushHost := registryHostFromPath(target.ImageHostPath())
	mirrorCA, caErr := resolveMirrorCA(name, ws, pushHost)
	if caErr != nil {
		return caErr // an explicit --registry-ca that can't be read is fatal
	}
	eng := registryEngine(target, in, mirrorCA)
	// Check the push credential once up front. Without this a wrong password is
	// retried against every artifact in the BOM (401 is retryable — Harbor's token
	// service genuinely flakes), so the command grinds for minutes and then reports
	// ~100 failures instead of one clear "the mirror rejected the credential".
	if err := eng.PreflightAuth(cmdContext(cmd), bom); err != nil {
		return err
	}
	results := eng.Replicate(cmdContext(cmd), bom)

	var failed int
	mirrored := make([]config.MirrorArtifact, 0, len(results))
	for _, r := range results {
		if r.Err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "  FAIL %s/%s:%s — %v\n", r.Artifact.Kind, r.Artifact.Name, r.Artifact.Tag, r.Err)
			continue
		}
		verb := "copied"
		if r.Skipped {
			verb = "skipped"
		}
		if !flagQuiet {
			fmt.Fprintf(os.Stderr, "  %s %s/%s:%s %s\n", verb, r.Artifact.Kind, r.Artifact.Name, r.Artifact.Tag, r.Digest)
		}
		mirrored = append(mirrored, config.MirrorArtifact{
			Kind: string(r.Artifact.Kind), Name: r.Artifact.Name, Tag: r.Artifact.Tag, Digest: r.Digest,
		})
	}

	rec := &config.RegistryMirror{
		Target:          registryTargetKind(ws),
		Namespace:       target.MirrorNamespace(),
		ChartHost:       target.ChartHostPath(),
		ImageHost:       target.ImageHostPath(),
		ManifestVersion: bom.ManifestVersion,
		Artifacts:       mirrored,
	}
	// Air-gap node trust: record the bare pull host and the CA it serves so
	// `bnk up` installs that CA on every node before pulling. The authoritative
	// copy (--registry-ca / registry.generic_ca_b64) wins; a captured CA must be
	// pinned. A public or unreachable host records no CA and node-trust no-ops.
	rec.RegistryHost = pushHost
	// mirrorCA was resolved (and trusted for the push) before the copy above.
	if mirrorCA != "" {
		rec.CACert = mirrorCA
		fmt.Fprintf(os.Stderr, "  ✓ trusted + recorded the mirror CA from %s (the push trusts it; nodes install it before pulling)\n", pushHost)
	} else if pushHost != "" {
		fmt.Fprintf(os.Stderr, "  ⚠ no private CA captured from %s — if it is a self-signed mirror, re-run with --registry-ca <file>\n", pushHost)
	}
	if err := config.WriteRegistryMirror(name, rec); err != nil {
		return fmt.Errorf("recording mirror: %w", err)
	}
	if failed > 0 {
		return fmt.Errorf("replicate: %d of %d artifacts failed", failed, len(results))
	}
	fmt.Fprintf(os.Stderr, "✓ mirrored %d artifacts into %s\n", len(mirrored), target.ChartHostPath())
	return nil
}
