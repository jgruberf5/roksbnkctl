package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/bnkbom"
	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// mirrorRecordMismatch reports why the recorded mirror does not describe the
// CONFIGURED target, or "" when it does (or when we cannot tell). It is the
// registry subcommands' entry to the shared check in internal/config, passing
// the `--target` flag so an explicit override is honoured here as everywhere
// else the flag applies.
//
// Cannot-tell is deliberately treated as a match. When the configured mirror
// cannot be resolved from config we know less than the record does, and
// discarding it on that basis would turn a diff into a full re-replicate for a
// reason that has nothing to do with the mirror's contents.
//
// ctx and name are unused: resolving the mirror's identity needs no client and
// no credentials. They stay in the signature because every call site is inside
// a cobra RunE that has them, and a check this cheap should not be the reason a
// caller has to restructure.
func mirrorRecordMismatch(_ context.Context, _ string, ws *config.Workspace, rec *config.RegistryMirror) string {
	return config.MirrorRecordMismatch(ws, rec, flagRegistryTarget)
}

func runRegistryDiff(cmd *cobra.Command, _ []string) error {
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
	have := map[string]bool{}
	if err == nil {
		// The record describes the mirror it was WRITTEN against, which is not
		// necessarily the one configured now. Re-point a workspace at a
		// different registry — or rebuild the one it names — and the record
		// still lists 89 artifacts that are not there, so diff reports "in
		// sync" against an EMPTY registry (#109). verify catches it because it
		// probes; diff does not probe at all.
		//
		// A record for a different mirror tells us nothing about this one, so it
		// is discarded rather than trusted. Everything then reads as missing,
		// which is the safe direction: it prompts a replicate, and replicate is
		// idempotent — an artifact already present at the right digest is
		// skipped.
		if why := mirrorRecordMismatch(cmdContext(cmd), name, ws, rec); why != "" {
			fmt.Fprintf(os.Stderr, "→ ignoring the recorded mirror: %s\n", why)
			fmt.Fprintln(os.Stderr, "  It describes a different mirror, so it says nothing about this one.")
		} else {
			for _, a := range rec.Artifacts {
				have[a.Kind+"|"+a.Name+":"+a.Tag] = true
			}
		}
	} else if err != config.ErrNoRegistryMirror {
		return err
	}

	var missing []bnkbom.Artifact
	for _, a := range bom.Artifacts {
		if !have[string(a.Kind)+"|"+a.Name+":"+a.Tag] {
			missing = append(missing, a)
		}
	}
	if flagOutput == "json" {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"missing": missing, "bom_total": len(bom.Artifacts)})
	}
	if len(missing) == 0 {
		// Say what this is based on. diff reads the RECORD; it never contacts
		// the registry, so "in sync" means "nothing left to replicate according
		// to what was last replicated" — not "every artifact is present". Only
		// verify establishes the latter, and the difference matters when the
		// registry has been emptied or rebuilt underneath the record.
		fmt.Fprintln(os.Stderr, "mirror is in sync with the BOM — nothing to replicate")
		fmt.Fprintln(os.Stderr, "  (from the recorded mirror; `registry verify` probes the registry itself)")
		return nil
	}
	fmt.Fprintf(os.Stderr, "%d of %d BOM artifacts not yet in the mirror:\n", len(missing), len(bom.Artifacts))
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KIND\tNAME\tTAG")
	for _, a := range missing {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", a.Kind, a.Name, a.Tag)
	}
	tw.Flush()
	return nil
}

// ── replicate ───────────────────────────────────────────────────────────────
