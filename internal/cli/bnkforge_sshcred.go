package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/forge"
)

// runBNKForgeSSHCredential gives Forge the PRIVATE key for an appliance this
// workspace built, and wires the project to use it (#222).
//
// This lives in roksbnkctl rather than in a Forge module step because it cannot
// live there: Forge requires container steps to be an argv vector and refuses a
// shell (module_metadata.py:794, "args must not invoke a shell"). The work is
// three calls in sequence -- log in, create-or-update the credential, update the
// project -- and the bearer token from the first has to reach the second's
// header. Nothing in an argv-only step can lift a field out of one JSON response
// into the next request. roksbnkctl is already the binary Forge can invoke as
// one argv, already holds the Forge session plumbing, and already knows the VSI
// it just created.
func runBNKForgeSSHCredential(ctx context.Context, interactive bool) error {
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}
	if cctx == nil || cctx.Workspace == nil {
		return fmt.Errorf("no workspace context")
	}
	// Effective config = stored block overlaid with flags, without persisting
	// them — the same rule `bnkforge register` follows.
	eff := config.BNKForgeCfg{}
	if cctx.Workspace.BNKForge != nil {
		eff = *cctx.Workspace.BNKForge
	}
	if flagBNKForgeURL != "" {
		eff.URL = flagBNKForgeURL
	}
	if flagBNKForgeUser != "" {
		eff.Username = flagBNKForgeUser
	}
	if flagBNKForgeInsecure {
		eff.Insecure = true
	}
	if flagBNKForgeCAFile != "" {
		b64, cerr := readForgeCAFile(flagBNKForgeCAFile)
		if cerr != nil {
			return cerr
		}
		eff.CAB64 = b64
	}
	// --password is a transient override, exposed via the env resolveForgePassword
	// already consults — same handling as `register`.
	if flagBNKForgePassword != "" {
		_ = os.Setenv(envForgePassword, flagBNKForgePassword)
	}
	bf := &eff

	keyPath := flagBNKForgeSSHKey
	if keyPath == "" {
		return fmt.Errorf("--key is required: the PRIVATE key Forge will use to reach the appliance\n" +
			"  (bnk.flp.vsi.ssh_key names a VPC key, which puts the PUBLIC half on the VSI —\n" +
			"   that is operator access; Forge needs the private half and nothing supplies it)")
	}
	if strings.HasPrefix(keyPath, "~/") {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return fmt.Errorf("expanding %q: %w", keyPath, herr)
		}
		keyPath = filepath.Join(home, keyPath[2:])
	}
	pem, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("reading private key %s: %w", keyPath, err)
	}

	// Verify the key BEFORE handing it over. See PrivateKeyFingerprint.
	fp, err := forge.PrivateKeyFingerprint(pem)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "  private key fingerprint: %s\n", fp)
	if want := flagBNKForgeSSHFingerprint; want != "" {
		if !forge.FingerprintsMatch(fp, want) {
			return fmt.Errorf("key fingerprint mismatch:\n  private key: %s\n  expected:    %s\n"+
				"Storing a key that cannot log in is worse than storing none — Forge would report "+
				"infrastructure access as configured and every later failure would point elsewhere", fp, want)
		}
		fmt.Fprintf(os.Stderr, "  matches the expected fingerprint\n")
	}

	host := flagBNKForgeSSHHost
	if host == "" {
		return fmt.Errorf("--host is required: the address FORGE must reach the appliance on\n" +
			"  Use the FLOATING IP. `flp status` reports a services-VPC endpoint (e.g. https://10.243.1.4:8443)\n" +
			"  which Forge sits outside of, so a credential built from it can never connect")
	}
	if strings.Contains(host, "://") {
		return fmt.Errorf("--host %q looks like an endpoint URL, not a host\n"+
			"  Pass the bare floating IP or hostname; the port is separate (--port)", host)
	}
	username := flagBNKForgeSSHUser

	client, err := newForgeClientForWorkspace(ctx, cctx, bf, interactive)
	if err != nil {
		return err
	}

	projName := flagBNKForgeProject
	if projName == "" {
		projName = bf.Project
	}
	if projName == "" {
		projName = cctx.WorkspaceName
	}
	pid, err := client.ProjectIDByName(ctx, projName)
	if err != nil {
		return fmt.Errorf("looking up project %q: %w", projName, err)
	}
	if pid == 0 {
		return fmt.Errorf("no BNK Forge project %q — run `roksbnkctl bnkforge register` first", projName)
	}

	credName := flagBNKForgeSSHName
	if credName == "" {
		credName = projName + "-ssh"
	}
	cid, err := client.EnsureSSHCredential(ctx, forge.SSHCredential{
		Name:        credName,
		Description: "Operator access to the appliance this project built",
		Host:        host,
		Port:        flagBNKForgeSSHPort,
		Username:    username,
		AuthType:    "key",
		PrivateKey:  string(pem),
	})
	if err != nil {
		return fmt.Errorf("creating the SSH credential: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ SSH credential %q (id %d) stored in BNK Forge\n", credName, cid)

	want := forge.ProjectInfraAccess{
		SSHCredentialID: cid,
		InfraEnabled:    true,
		InfraHost:       host,
		InfraUsername:   username,
		InfraPort:       flagBNKForgeSSHPort,
		InfraAuthType:   "key",
	}
	got, err := client.AttachSSHCredential(ctx, pid, want)
	if err != nil {
		return fmt.Errorf("attaching the credential to project %q: %w", projName, err)
	}

	if got.Matches(want) {
		fmt.Fprintf(os.Stderr, "✓ project %q (id %d) is wired for infrastructure access\n", projName, pid)
		return nil
	}

	// The write returned 200 and Forge kept only part of it. Say exactly which
	// part, because the visible symptom otherwise is an appliance that stays
	// unreachable with nothing pointing at the cause.
	fmt.Fprintf(os.Stderr, "⚠ BNK Forge accepted the update but did not store all of it.\n")
	if got.SSHCredentialID == cid {
		fmt.Fprintf(os.Stderr, "    ssh_credential_id  = %d          (stored)\n", got.SSHCredentialID)
	} else {
		fmt.Fprintf(os.Stderr, "    ssh_credential_id  = %d          (wanted %d)\n", got.SSHCredentialID, cid)
	}
	fmt.Fprintf(os.Stderr, "    infra_enabled      = %v       (wanted true)\n", got.InfraEnabled)
	fmt.Fprintf(os.Stderr, "    infra_host         = %q     (wanted %q)\n", got.InfraHost, host)
	fmt.Fprintf(os.Stderr, "    infra_ssh_username = %q     (wanted %q)\n", got.InfraUsername, username)
	fmt.Fprintf(os.Stderr, "    infra_auth_type    = %q     (wanted \"key\")\n", got.InfraAuthType)
	fmt.Fprintf(os.Stderr,
		"  This is a BNK Forge limitation, not a failure here: PUT /api/projects/<id> applies\n"+
			"  ssh_credential_id and silently discards the infra_* fields, PATCH is 405, and there is\n"+
			"  no /api/projects/<id>/infrastructure endpoint. Until Forge exposes a path for them the\n"+
			"  appliance stays unreachable even though the credential is stored. Tracked in #222.\n")
	return nil
}
