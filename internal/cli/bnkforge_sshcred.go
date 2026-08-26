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

	if verr := validateSSHCredentialInputs(flagBNKForgeSSHHost, flagBNKForgeSSHUser, flagBNKForgeSSHKey); verr != nil {
		return verr
	}
	keyPath := flagBNKForgeSSHKey
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

	// Infrastructure access is turned on by its OWN endpoint. #222 recorded this
	// as blocked upstream because PUT /api/projects/<id> ignores the infra_*
	// fields — true, but they are not that route's to set. Forge owns them at
	// POST /api/cloud-auth/ssh/configure, which also TESTS the connection before
	// storing anything, so a key that cannot open the box is refused at the
	// source rather than stored and discovered later.
	if cerr := client.ConfigureSSH(ctx, forge.ConfigureSSHRequest{
		ProjectID:  pid,
		Host:       host,
		Port:       flagBNKForgeSSHPort,
		Username:   username,
		AuthType:   "key",
		PrivateKey: string(pem),
	}); cerr != nil {
		return fmt.Errorf("configuring infrastructure access on project %q: %w\n"+
			"  Forge tests the SSH connection before storing it, so this usually means the key or "+
			"host is wrong rather than the request", projName, cerr)
	}

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

	// Still read back and still report a partial result as partial. Both halves
	// are now written through endpoints that own them, so a mismatch here is a
	// real discrepancy rather than an expected limitation.
	fmt.Fprintf(os.Stderr, "⚠ BNK Forge did not store everything that was sent:\n")
	fmt.Fprintf(os.Stderr, "    ssh_credential_id  = %d          (wanted %d)\n", got.SSHCredentialID, cid)
	fmt.Fprintf(os.Stderr, "    infra_enabled      = %v       (wanted true)\n", got.InfraEnabled)
	fmt.Fprintf(os.Stderr, "    infra_host         = %q     (wanted %q)\n", got.InfraHost, host)
	fmt.Fprintf(os.Stderr, "    infra_ssh_username = %q     (wanted %q)\n", got.InfraUsername, username)
	fmt.Fprintf(os.Stderr, "    infra_auth_type    = %q     (wanted \"key\")\n", got.InfraAuthType)
	return nil
}

// validateSSHCredentialInputs rejects the three inputs that produce a credential
// Forge stores happily and can never use.
//
// All three fail the same way if they get through: Forge reports infrastructure
// access as CONFIGURED, and every later failure points somewhere other than the
// credential. That is why they are refused here rather than left to the server.
func validateSSHCredentialInputs(host, username, keyPath string) error {
	if keyPath == "" {
		return fmt.Errorf("--key is required: the PRIVATE key Forge will use to reach the appliance\n" +
			"  (bnk.flp.vsi.ssh_key names a VPC key, which puts the PUBLIC half on the VSI —\n" +
			"   that is operator access; Forge needs the private half and nothing supplies it)")
	}
	if host == "" {
		return fmt.Errorf("--host is required: the address FORGE must reach the appliance on\n" +
			"  Use the FLOATING IP. `flp status` reports a services-VPC endpoint (e.g. https://10.243.1.4:8443)\n" +
			"  which Forge sits outside of, so a credential built from it can never connect")
	}
	if strings.Contains(host, "://") {
		return fmt.Errorf("--host %q looks like an endpoint URL, not a host\n"+
			"  Pass the bare floating IP or hostname; the port is separate (--port)", host)
	}
	if strings.TrimSpace(username) == "" {
		return fmt.Errorf("--ssh-username cannot be empty: Forge would store a credential with no " +
			"user, which can never authenticate and which Forge still reports as configured access")
	}
	return nil
}
