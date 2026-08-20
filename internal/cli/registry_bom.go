package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

func runRegistryBOM(cmd *cobra.Command, _ []string) error {
	name, ws, err := loadRegistryWorkspace()
	if err != nil {
		return err
	}
	in := resolveBOMInputs(ws)
	bom, err := buildBOM(cmdContext(cmd), name, ws, &in, registryScratchDir(name))
	if err != nil {
		return err
	}
	if flagRegistryJSON || flagOutput == "json" {
		return json.NewEncoder(os.Stdout).Encode(bom)
	}
	printBOMTable(bom)
	return nil
}

func runRegistryList(_ *cobra.Command, _ []string) error {
	name, _, err := loadRegistryWorkspace()
	if err != nil {
		return err
	}
	rec, err := config.ReadRegistryMirror(name)
	if err != nil {
		return err
	}
	if flagOutput == "json" {
		return json.NewEncoder(os.Stdout).Encode(rec)
	}
	fmt.Fprintf(os.Stderr, "mirror %s/%s (manifest %s, recorded %s) — %d artifacts\n",
		rec.Target, rec.Namespace, rec.ManifestVersion, rec.RecordedAt.Format(time.RFC3339), len(rec.Artifacts))
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KIND\tNAME\tTAG\tDIGEST")
	for _, a := range rec.Artifacts {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", a.Kind, a.Name, a.Tag, a.Digest)
	}
	tw.Flush()
	return nil
}

// ── diff ────────────────────────────────────────────────────────────────────
