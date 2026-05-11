package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/doctor"
	"github.com/jgruberf5/roksbnkctl/internal/remote"
)

// DocsURL is the canonical user documentation surface for roksbnkctl —
// the published mdBook at GitHub Pages. Single source of truth so the
// `version` subcommand, the cobra-wired `--version` flag, and the
// `self update` flow all surface the same URL.
const DocsURL = "https://jgruberf5.github.io/roksbnkctl/book/"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version, commit, and build date",
	RunE: func(cmd *cobra.Command, _ []string) error {
		// Keep the first line byte-identical to the pre-v1.0 shape so
		// any scripts that grep `roksbnkctl version` output for the
		// "(commit X, built Y)" tail continue to parse. Append the
		// docs URL on its own second line.
		fmt.Fprintf(cmd.OutOrStdout(), "roksbnkctl %s (commit %s, built %s)\nDocs: %s\n",
			Version, Commit, BuildDate, DocsURL)
		return nil
	},
}

var selfCmd = &cobra.Command{
	Use:   "self",
	Short: "Manage the roksbnkctl binary itself",
}

var selfUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Pull the latest roksbnkctl release matching the host arch",
	Long: `Downloads the latest GitHub release tarball for this platform,
verifies its SHA256 against the release's checksums.txt, and replaces
the running binary in place.

Linux/macOS only — Windows can't replace a running .exe in place; use
` + "`scoop update roksbnkctl`" + ` instead.

Requires write permission on the binary's directory (typical install
under /usr/local/bin needs sudo; brew/scoop should use their own
upgrade verb).`,
	RunE: runSelfUpdate,
}

// flagDoctorTarget — when set, doctor adds an extra Check that runs a
// no-op `whoami` on the named target (PRD 01 §11). The Check uses
// BackendName="" today; Phase 3 (PRD 03) will set "ssh" once SSH is a
// proper backend.
var flagDoctorTarget string

// flagDoctorBackend — when set, doctor runs the per-backend availability
// checks defined in PRD 03 §"doctor extensions" (k8s ops pod + RBAC,
// ssh:<target> reachability + bootstrap feasibility). Empty preserves
// Sprint 0+ behaviour.
var flagDoctorBackend string

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check prerequisites and report missing pieces",
	Long: `Verifies the host has what roksbnkctl needs.

Required (hard fail on missing):
  - terraform on PATH (the local backend's workhorse for ` + "`roksbnkctl up`" + `)

Informational (the binary internalises each surface; missing → no warning):
  - kubectl / oc — internalised via client-go (` + "`roksbnkctl k *`" + `)
  - ibmcloud     — bundled image, run via --backend docker / --backend ssh:<target>
  - iperf3       — bundled image, run via --backend k8s
  - dig          — DNS probe internalised via miekg/dns

A stock dev box with only ` + "`terraform`" + ` installed should produce exit 0
and zero warnings.

Pass --target <name> to additionally probe an SSH target (runs whoami).
Pass --backend k8s | ssh:<target> for per-backend prereq checks.

Exits non-zero only when a required check fails (warnings don't block).`,
	RunE: runDoctor,
}

func init() {
	doctorCmd.Flags().StringVar(&flagDoctorTarget, "target", "", "additionally probe the named SSH target with `whoami`")
	doctorCmd.Flags().StringVar(&flagDoctorBackend, "backend", "", "additionally run per-backend checks: k8s | ssh:<target>")
	selfCmd.AddCommand(selfUpdateCmd)
	rootCmd.AddCommand(versionCmd, selfCmd, doctorCmd)
}

// runDoctor loads the workspace context (best-effort — doctor still runs
// usefully even when no workspace is initialised) and prints the report.
func runDoctor(cmd *cobra.Command, _ []string) error {
	// config.New tolerates a missing workspace; doctor's check methods
	// downgrade workspace-dependent checks accordingly.
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		// Even an unreadable global config shouldn't kill doctor — emit
		// what we can.
		fmt.Fprintf(os.Stderr, "warning: loading global config: %v\n", err)
		cctx = &config.Context{WorkspaceName: "(unknown)"}
	}

	results := doctor.Run(cmd.Context(), cctx)
	if flagDoctorTarget != "" {
		results = append(results, runTargetCheck(cmd.Context(), cctx, flagDoctorTarget))
	}
	// Sprint 5: DNS probe sanity check. Runs the in-process miekg/dns
	// probe against the workspace's `test.dns.default_target` (or
	// skips silently if not configured). Mostly a no-op since the
	// probe library is built into the binary, but useful for
	// surfacing "DNS resolution latency" alongside the other doctor
	// metrics.
	if c, ok := runDNSProbeCheck(cmd.Context(), cctx); ok {
		results = append(results, c)
	}
	if flagDoctorBackend != "" {
		results = append(results, runBackendChecks(cmd.Context(), cctx, flagDoctorBackend)...)
	}
	if err := doctor.PrintResults(os.Stdout, results); err != nil {
		return err
	}
	if doctor.HasFailures(results) {
		os.Exit(1)
	}
	return nil
}

// runTargetCheck runs `whoami` against the named target and reports it
// as a doctor.Check. Treated as a single Check rather than a stream so
// the existing PrintResults rendering doesn't change for the
// no-target case (preserves Sprint 0's byte-equivalence).
//
// BackendName is "" today; Phase 3 (PRD 03) will switch to "ssh" once
// the backend abstraction lands. Until then the Check renders without a
// backend prefix, identical to the general checks.
func runTargetCheck(ctx context.Context, cctx *config.Context, name string) doctor.Check {
	c := doctor.Check{
		Name:        "target " + name,
		BackendName: "", // TODO(phase3): set "ssh" once PRD 03 backend lands
	}
	if cctx == nil || cctx.Workspace == nil {
		c.Status = doctor.StatusError
		c.Detail = "no workspace"
		return c
	}
	t, err := remote.LoadTarget(cctx.WorkspaceName, name)
	if err != nil {
		c.Status = doctor.StatusError
		if errors.Is(err, remote.ErrTargetNotFound) {
			c.Detail = "not in targets: (try `roksbnkctl targets list`)"
		} else {
			c.Detail = err.Error()
		}
		return c
	}
	tfOutputs, err := loadTFOutputsForTarget(ctx, cctx, t)
	if err != nil {
		c.Status = doctor.StatusError
		c.Detail = "tf outputs: " + err.Error()
		return c
	}
	signer, err := remote.ResolveSigner(t, tfOutputs)
	if err != nil {
		c.Status = doctor.StatusError
		c.Detail = "key: " + err.Error()
		return c
	}
	t.Signer = signer
	t.HostKeyCallback = remote.HostKeyCallback(remote.HostKeyOptions{Insecure: flagInsecureHostKey})

	client, err := remote.Connect(ctx, t)
	if err != nil {
		c.Status = doctor.StatusError
		c.Detail = "connect: " + err.Error()
		return c
	}
	defer client.Close()

	var stdout, stderr bytes.Buffer
	code, err := client.Run(ctx, []string{"whoami"}, remote.RunOpts{
		Stdout: &stdout, Stderr: &stderr,
	})
	if err != nil {
		c.Status = doctor.StatusError
		c.Detail = "whoami: " + err.Error()
		return c
	}
	if code != 0 {
		c.Status = doctor.StatusError
		c.Detail = fmt.Sprintf("whoami exited %d (stderr: %q)", code, stderr.String())
		return c
	}
	c.Status = doctor.StatusOK
	c.Detail = fmt.Sprintf("%s@%s → %s", t.User, t.Host, trimTrailingNewline(stdout.String()))
	return c
}

func trimTrailingNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
