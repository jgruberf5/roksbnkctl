package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/registry/mirror"
)

// recordedDigests returns the artifact digests this workspace's mirror record
// carries, keyed by mirror.RecordedKey. Empty (never nil-checked by callers)
// when there is no record, no inventory, or the record cannot be read -- all of
// which mean "fall back to comparing against the source".
func recordedDigests(workspace string) map[string]string {
	rec, err := config.ReadRegistryMirror(workspace)
	if err != nil || rec == nil {
		return nil
	}
	out := make(map[string]string, len(rec.Artifacts))
	for _, a := range rec.Artifacts {
		if a.Digest == "" {
			continue
		}
		out[a.Kind+"/"+a.Name+":"+a.Tag] = a.Digest
	}
	return out
}

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

	// Verify against the digests replication recorded, when this workspace has a
	// record. Comparing to a freshly-resolved SOURCE digest asks "does the mirror
	// match upstream right now", which is not the question here: upstream is
	// allowed to move, and an air-gapped mirror cannot reach it at all. A mirror
	// that is exactly what was replicated must verify (#270).
	//
	// No record, or an artifact missing from it, falls back to the source
	// comparison, so nothing that verified before stops verifying.
	recorded := recordedDigests(name)
	var bad []mirror.Result
	for _, r := range eng.VerifyAllRecorded(cmdContext(cmd), bom, recorded) {
		if r.Err != nil {
			bad = append(bad, r)
		}
	}
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
