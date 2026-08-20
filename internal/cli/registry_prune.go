package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

func runRegistryPrune(cmd *cobra.Command, _ []string) error {
	name, ws, err := loadRegistryWorkspace()
	if err != nil {
		return err
	}
	in := resolveBOMInputs(ws)
	bom, err := buildBOM(cmdContext(cmd), name, ws, &in, registryScratchDir(name))
	if err != nil {
		return err
	}
	rec, err := config.ReadRegistryMirror(name)
	if err != nil {
		return err
	}

	// Same refusal as delete: prune computes what to REMOVE from the record and
	// removes it from the configured target. A record for another mirror makes
	// that a delete against the wrong registry (#109).
	if why := mirrorRecordMismatch(cmdContext(cmd), name, ws, rec); why != "" {
		return fmt.Errorf("refusing to prune: the recorded mirror does not describe the configured target — %s", why)
	}
	inBOM := map[string]bool{}
	for _, a := range bom.Artifacts {
		inBOM[string(a.Kind)+"|"+a.Name+":"+a.Tag] = true
	}
	var stale []config.MirrorArtifact
	for _, a := range rec.Artifacts {
		if !inBOM[a.Kind+"|"+a.Name+":"+a.Tag] {
			stale = append(stale, a)
		}
	}
	if flagOutput == "json" {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"stale": stale})
	}
	if len(stale) == 0 {
		fmt.Fprintln(os.Stderr, "no stale artifacts in the mirror — nothing to prune")
		return nil
	}
	// Pruning a registry target is a per-artifact manifest delete; here we
	// report the stale set so an operator can act. Removing them from the record
	// keeps the mirror record honest about the intended set.
	fmt.Fprintf(os.Stderr, "%d stale artifacts (no longer in the BOM):\n", len(stale))
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KIND\tNAME\tTAG")
	for _, a := range stale {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", a.Kind, a.Name, a.Tag)
	}
	tw.Flush()
	return nil
}
