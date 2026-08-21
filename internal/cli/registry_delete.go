package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/bnkbom"
	"github.com/jgruberf5/roksbnkctl/internal/config"
)

func runRegistryDelete(cmd *cobra.Command, _ []string) error {
	name, ws, err := loadRegistryWorkspace()
	if err != nil {
		return err
	}
	rec, err := config.ReadRegistryMirror(name)
	if err != nil {
		if errors.Is(err, config.ErrNoRegistryMirror) {
			fmt.Fprintln(os.Stderr, "no mirror recorded — nothing to delete")
			return nil
		}
		return err
	}
	// REFUSE, not discard. delete takes its artifact list from the RECORD and
	// removes it from the CONFIGURED target, so a record describing another
	// mirror deletes one registry's contents out of a different one — and the
	// prompt below names the record's host, so it would state the wrong
	// destination while doing it. diff can afford to shrug this off and report
	// everything missing; an unrecoverable delete cannot (#109).
	if why := mirrorRecordMismatch(cmdContext(cmd), name, ws, rec); why != "" {
		return fmt.Errorf("refusing to delete: the recorded mirror does not describe the configured target — %s.\n"+
			"  Deleting would remove that mirror's artifact list from THIS one.\n"+
			"  Point the workspace back at the recorded mirror, or clear the record with `registry adopt`", why)
	}
	if len(rec.Artifacts) == 0 {
		fmt.Fprintln(os.Stderr, "mirror is empty — nothing to delete")
		return nil
	}
	if !flagRegistryForce {
		if !promptYesNo(fmt.Sprintf("Delete all %d replicated artifact(s) from the %s target (%s)?", len(rec.Artifacts), rec.Target, rec.ImageHost), false) {
			return errors.New("aborted")
		}
	}
	target, err := buildTarget(cmdContext(cmd), name, ws)
	if err != nil {
		return err
	}
	arts := make([]bnkbom.Artifact, len(rec.Artifacts))
	for i, ma := range rec.Artifacts {
		arts[i] = bnkbom.Artifact{Name: ma.Name, Tag: ma.Tag, Digest: ma.Digest}
	}
	// Delete talks to the mirror too, so it needs the same CA the record carries.
	// The recorded CACert is authoritative here: it is what replicate/adopt already
	// established for this mirror, so no rediscovery (or pin prompt) is needed.
	delCA := rec.CACert
	if delCA == "" {
		delCA, _ = resolveMirrorCA(name, ws, registryHostFromPath(target.ImageHostPath()))
	}
	results := registryEngine(target, resolveBOMInputs(ws), delCA).Delete(cmdContext(cmd), arts)

	var deleted, failed int
	var remaining []config.MirrorArtifact
	for i, r := range results {
		if r.Err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "  FAIL %s:%s — %v\n", r.Artifact.Name, r.Artifact.Tag, r.Err)
			remaining = append(remaining, rec.Artifacts[i])
			continue
		}
		deleted++
		if !flagQuiet {
			fmt.Fprintf(os.Stderr, "  deleted %s:%s\n", r.Artifact.Name, r.Artifact.Tag)
		}
	}
	fmt.Fprintf(os.Stderr, "✓ deleted %d artifact(s)\n", deleted)

	// Drop the record when the mirror is empty; otherwise keep the artifacts
	// that failed so a re-run retries exactly those.
	if len(remaining) == 0 {
		if derr := config.DeleteRegistryMirror(name); derr != nil {
			return derr
		}
	} else {
		rec.Artifacts = remaining
		if werr := config.WriteRegistryMirror(name, rec); werr != nil {
			return werr
		}
	}
	if failed > 0 {
		return fmt.Errorf("delete: %d artifact(s) could not be removed", failed)
	}
	return nil
}
