package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

func runRegistryVerify(cmd *cobra.Command, _ []string) error {
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
	// Trust a private/self-signed mirror's CA for the verify HEAD checks too — the
	// crane digest probes fail x509 from a container operator otherwise, exactly as
	// the replicate push does. Best-effort capture (public targets return "").
	verifyCA, _ := resolveMirrorCA(name, ws, registryHostFromPath(target.ImageHostPath()))
	eng := registryEngine(target, in, verifyCA)
	bad := eng.Verify(cmdContext(cmd), bom)
	if flagOutput == "json" {
		out := make([]map[string]string, 0, len(bad))
		for _, b := range bad {
			out = append(out, map[string]string{"name": b.Artifact.Name, "tag": b.Artifact.Tag, "error": b.Err.Error()})
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"bad": out, "bom_total": len(bom.Artifacts)})
	}
	if len(bad) == 0 {
		fmt.Fprintf(os.Stderr, "✓ all %d BOM artifacts present + digest-matched in the mirror\n", len(bom.Artifacts))
		// Verify stays read-only. It does NOT write registry-mirror.json — a verb
		// that promises inspection should not change what a later `bnk up` does,
		// and two commands writing the record would drift over what they put in it
		// (replicate and adopt --verify-contents record an artifact inventory; a
		// bare adopt cannot). It does say what to run, so a mirror proven good is
		// one obvious command away from being usable.
		if _, rerr := config.ReadRegistryMirror(name); errors.Is(rerr, config.ErrNoRegistryMirror) {
			fmt.Fprintln(os.Stderr, "  note: this workspace has no mirror record, so `bnk up` will refuse to "+
				"use it. Run `roksbnkctl registry adopt` to record it.")
		}
		return nil
	}
	for _, b := range bad {
		fmt.Fprintf(os.Stderr, "  BAD %s/%s:%s — %v\n", b.Artifact.Kind, b.Artifact.Name, b.Artifact.Tag, b.Err)
	}
	return fmt.Errorf("verify: %d of %d artifacts missing or mismatched", len(bad), len(bom.Artifacts))
}

// ── prune ───────────────────────────────────────────────────────────────────
