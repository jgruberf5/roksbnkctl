package forge

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net/http/httptest"
	"testing"

	"golang.org/x/crypto/ssh"
)

// ── #223: the provider value Forge actually matches ──────────────────────────

// Forge compares `provider == "ibm"` in at least seven places and lowercases
// none of them. "IBM" is accepted by the API and then matches nothing: no
// credential injection, no blueprint input resolution, no IBM lookup, and the
// "IBM templates must carry an API key" validation never fires. Nothing errors —
// the template just does nothing.
func TestTheCredentialTemplateIsCreatedWithTheProviderForgeMatches(t *testing.T) {
	m := &mockForge{token: "t", nextID: 40}
	srv := httptest.NewServer(m.handler(t))
	defer srv.Close()
	c := mustNew(t, srv.URL, Options{})
	c.Token = "t"

	if _, err := c.EnsureIBMCredentialTemplate(context.Background(), IBMCredentialTemplate{
		Name: "roksbnkctl-ws", APIKey: "K", ResourceGroup: "default",
		Region: "us-east", COSInstance: "bnk-orchestration",
	}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if len(m.credTemplates) != 1 {
		t.Fatalf("expected one created template, got %v", m.credTemplates)
	}
	got := m.credTemplates[0]
	if got["provider"] != "ibm" {
		t.Errorf(`provider = %q, want "ibm" exactly.`+"\n"+
			`Forge matches this case-sensitively; "IBM" is stored happily and then matches `+
			`nothing, so the template is inert in both directions (#223).`, got["provider"])
	}
	if got["region"] != "us-east" {
		t.Errorf(`region = %v, want "us-east" — blueprint inputs with `+
			"`source: credential_template, source_field: region` have nothing to inherit otherwise", got["region"])
	}
	if got["ibm_cos_instance_name"] != "bnk-orchestration" {
		t.Errorf("ibm_cos_instance_name = %v, want bnk-orchestration", got["ibm_cos_instance_name"])
	}
}

// The reported symptom is a template that ALREADY exists with provider "IBM".
// Fixing only the create path would repair new installs and abandon every
// existing one — including the one in the issue.
func TestAnExistingTemplateIsRepairedRatherThanLeftInert(t *testing.T) {
	m := &mockForge{
		token:         "t",
		nextID:        40,
		credTemplates: []map[string]any{{"id": 3, "name": "roksbnkctl-bnk", "provider": "IBM"}},
	}
	srv := httptest.NewServer(m.handler(t))
	defer srv.Close()
	c := mustNew(t, srv.URL, Options{})
	c.Token = "t"

	id, err := c.EnsureIBMCredentialTemplate(context.Background(), IBMCredentialTemplate{
		Name: "roksbnkctl-bnk", APIKey: "K", ResourceGroup: "default",
		Region: "us-east", COSInstance: "bnk-orchestration",
	})
	if err != nil || id != 3 {
		t.Fatalf("ensure existing: id=%d err=%v (want 3)", id, err)
	}
	if m.credUpdate == nil {
		t.Fatal("no PUT was sent to the existing template")
	}
	if m.credUpdate["provider"] != "ibm" {
		t.Errorf(`the update sent provider = %v, want "ibm".`+"\n"+
			`Without it a template a previous roksbnkctl wrote as "IBM" stays broken forever, `+
			`and that template is the reported defect (#223).`, m.credUpdate["provider"])
	}
	if m.credUpdate["region"] != "us-east" || m.credUpdate["ibm_cos_instance_name"] != "bnk-orchestration" {
		t.Errorf("the update did not backfill region/cos: %v", m.credUpdate)
	}
}

// An unset region must not be sent. Writing "" would overwrite a value an
// operator set by hand with an empty one, which is worse than leaving it.
func TestUnsetFieldsAreNotSentAsEmptyStrings(t *testing.T) {
	m := &mockForge{token: "t", nextID: 40}
	srv := httptest.NewServer(m.handler(t))
	defer srv.Close()
	c := mustNew(t, srv.URL, Options{})
	c.Token = "t"

	if _, err := c.EnsureIBMCredentialTemplate(context.Background(), IBMCredentialTemplate{
		Name: "roksbnkctl-ws", APIKey: "K", ResourceGroup: "default",
	}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	got := m.credTemplates[0]
	if _, present := got["region"]; present {
		t.Errorf("region was sent as %v despite being unset — that overwrites a hand-set value", got["region"])
	}
	if _, present := got["ibm_cos_instance_name"]; present {
		t.Errorf("ibm_cos_instance_name was sent despite being unset: %v", got["ibm_cos_instance_name"])
	}
	if got["provider"] != "ibm" {
		t.Errorf("provider = %v, want ibm even with the optional fields absent", got["provider"])
	}
}

// ── #222: SSH credentials ────────────────────────────────────────────────────

func TestTheSSHCredentialCarriesThePrivateKeyAndDefaults(t *testing.T) {
	m := &mockForge{token: "t", nextID: 90}
	srv := httptest.NewServer(m.handler(t))
	defer srv.Close()
	c := mustNew(t, srv.URL, Options{})
	c.Token = "t"

	id, err := c.EnsureSSHCredential(context.Background(), SSHCredential{
		Name: "bnk-ssh", Host: "52.116.1.2", Username: "ubuntu", PrivateKey: "PEMDATA",
	})
	if err != nil || id == 0 {
		t.Fatalf("ensure ssh cred: id=%d err=%v", id, err)
	}
	if m.sshCreate["private_key"] != "PEMDATA" {
		t.Errorf("the private key did not reach Forge: %v", m.sshCreate)
	}
	if m.sshCreate["has_private_key"] != true {
		t.Errorf("Forge did not record has_private_key: %v", m.sshCreate)
	}
	// Defaults matter: auth_type defaults to "password" on the Forge side, and a
	// key credential stored as a password one cannot log in.
	if m.sshCreate["auth_type"] != "key" {
		t.Errorf(`auth_type = %v, want "key"`, m.sshCreate["auth_type"])
	}
	if m.sshCreate["port"] != float64(22) {
		t.Errorf("port = %v, want 22", m.sshCreate["port"])
	}
}

// Re-running must update the existing credential rather than pile up duplicates
// or fail — the subcommand is expected to be re-run after a rebuild.
func TestASecondRunUpdatesTheExistingCredential(t *testing.T) {
	m := &mockForge{
		token:    "t",
		nextID:   90,
		sshCreds: []map[string]any{{"id": 11, "name": "bnk-ssh"}},
	}
	srv := httptest.NewServer(m.handler(t))
	defer srv.Close()
	c := mustNew(t, srv.URL, Options{})
	c.Token = "t"

	id, err := c.EnsureSSHCredential(context.Background(), SSHCredential{
		Name: "bnk-ssh", Host: "52.116.1.9", Username: "ubuntu", PrivateKey: "NEWPEM",
	})
	if err != nil || id != 11 {
		t.Fatalf("update path: id=%d err=%v (want 11)", id, err)
	}
	if m.sshCreate != nil {
		t.Error("a duplicate credential was created instead of updating the existing one")
	}
	if m.sshUpdate["host"] != "52.116.1.9" || m.sshUpdate["private_key"] != "NEWPEM" {
		t.Errorf("the update did not carry the new host/key: %v", m.sshUpdate)
	}
}

// THE FIELDS HAVE AN OWNER, AND IT IS NOT THE PROJECTS ROUTE.
//
// #222 recorded infra_enabled / infra_host / infra_ssh_username /
// infra_auth_type as unsettable because PUT /api/projects/<id> ignores them.
// True — they are not that route's to set. Forge owns them at
// POST /api/cloud-auth/ssh/configure. Nothing was missing upstream; roksbnkctl
// was asking the wrong endpoint.
func TestConfigureSSHTurnsOnInfrastructureAccess(t *testing.T) {
	m := &mockForge{token: "t", nextID: 90}
	srv := httptest.NewServer(m.handler(t))
	defer srv.Close()
	c := mustNew(t, srv.URL, Options{})
	c.Token = "t"

	err := c.ConfigureSSH(context.Background(), ConfigureSSHRequest{
		ProjectID: 7, Host: "52.116.1.2", Username: "ubuntu", PrivateKey: "PEMDATA",
	})
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	if m.sshConfigure == nil {
		t.Fatal("POST /api/cloud-auth/ssh/configure was never called — the projects route does " +
			"not own these fields, so writing there alone leaves the appliance unreachable")
	}
	if m.sshConfigure["private_key"] != "PEMDATA" {
		t.Errorf("the private key did not reach the endpoint: %v", m.sshConfigure)
	}
	// auth_type is pattern-validated server-side (^(password|key)$) and defaults
	// to "password" on the project; a key credential stored as a password one
	// cannot log in.
	if m.sshConfigure["auth_type"] != "key" {
		t.Errorf(`auth_type = %v, want "key"`, m.sshConfigure["auth_type"])
	}
	if m.sshConfigure["port"] != float64(22) {
		t.Errorf("port = %v, want the 22 default", m.sshConfigure["port"])
	}
}

// Forge tests the SSH connection before storing and answers 400 when it fails.
// That must surface, not be swallowed — it is the strongest signal available
// that the key or host is wrong.
func TestAFailedConnectionTestIsReported(t *testing.T) {
	m := &mockForge{token: "t", nextID: 90, sshConfigureStatus: 400}
	srv := httptest.NewServer(m.handler(t))
	defer srv.Close()
	c := mustNew(t, srv.URL, Options{})
	c.Token = "t"

	err := c.ConfigureSSH(context.Background(), ConfigureSSHRequest{
		ProjectID: 7, Host: "52.116.1.2", Username: "ubuntu", PrivateKey: "BAD",
	})
	if err == nil {
		t.Fatal("a 400 from the connection test was swallowed — the caller would report success " +
			"on a credential Forge refused to store")
	}
}

// After both halves — the credential link and the infrastructure config — the
// read-back must agree with what was asked for.
func TestBothHalvesTogetherLeaveTheProjectConfigured(t *testing.T) {
	m := &mockForge{token: "t", nextID: 90}
	srv := httptest.NewServer(m.handler(t))
	defer srv.Close()
	c := mustNew(t, srv.URL, Options{})
	c.Token = "t"
	ctx := context.Background()

	if err := c.ConfigureSSH(ctx, ConfigureSSHRequest{
		ProjectID: 7, Host: "52.116.1.2", Username: "ubuntu", PrivateKey: "PEM",
	}); err != nil {
		t.Fatalf("configure: %v", err)
	}
	want := ProjectInfraAccess{
		SSHCredentialID: 11, InfraEnabled: true,
		InfraHost: "52.116.1.2", InfraUsername: "ubuntu",
	}
	got, err := c.AttachSSHCredential(ctx, 7, want)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if !got.Matches(want) {
		t.Errorf("after both calls the project is still not configured: %+v", got)
	}
}

// The credential link alone is NOT enough, and must not read as success. This is
// the original symptom: ssh_credential_id set, infra_enabled false, appliance
// unreachable.
func TestTheCredentialLinkAloneIsNotReportedAsConfigured(t *testing.T) {
	m := &mockForge{token: "t", nextID: 90}
	srv := httptest.NewServer(m.handler(t))
	defer srv.Close()
	c := mustNew(t, srv.URL, Options{})
	c.Token = "t"

	want := ProjectInfraAccess{
		SSHCredentialID: 11, InfraEnabled: true,
		InfraHost: "52.116.1.2", InfraUsername: "ubuntu",
	}
	// ConfigureSSH deliberately NOT called.
	got, err := c.AttachSSHCredential(context.Background(), 7, want)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if got.SSHCredentialID != 11 {
		t.Errorf("ssh_credential_id = %d, want 11 — that half does apply", got.SSHCredentialID)
	}
	if got.Matches(want) {
		t.Error("reported the project as configured with only the credential linked.\n" +
			"infra_enabled is still false and the appliance is still unreachable (#222).")
	}
}

// ── fingerprints ─────────────────────────────────────────────────────────────

func testKeyPEM(t *testing.T) ([]byte, string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	blk, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(blk)
	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		t.Fatal(err)
	}
	return pemBytes, ssh.FingerprintSHA256(signer.PublicKey())
}

// The fingerprint must equal what ssh-keygen and IBM Cloud report, or every
// comparison against a VPC key fails and the check is worse than useless.
func TestTheFingerprintMatchesTheOpenSSHForm(t *testing.T) {
	pemBytes, want := testKeyPEM(t)
	got, err := PrivateKeyFingerprint(pemBytes)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	if got != want {
		t.Errorf("fingerprint = %q, want %q (the ssh-keygen -l -E sha256 form)", got, want)
	}
}

// OpenSSH prints the base64 unpadded and some tools pad it; the prefix is
// likewise inconsistent. A comparison that trips on either would reject a key
// that is in fact correct.
func TestFingerprintComparisonToleratesPrefixAndPadding(t *testing.T) {
	cases := [][2]string{
		{"SHA256:abc", "abc"},
		{"SHA256:abc=", "SHA256:abc"},
		{"sha256:abc", "SHA256:abc"},
		{" SHA256:abc ", "abc"},
	}
	for _, c := range cases {
		if !FingerprintsMatch(c[0], c[1]) {
			t.Errorf("FingerprintsMatch(%q, %q) = false, want true", c[0], c[1])
		}
	}
	if FingerprintsMatch("SHA256:abc", "SHA256:xyz") {
		t.Error("two different fingerprints compared equal")
	}
}

// A garbage key must be rejected here, not stored and discovered later.
func TestAnUnparseableKeyIsRejected(t *testing.T) {
	if _, err := PrivateKeyFingerprint([]byte("not a key")); err == nil {
		t.Error("an unparseable private key was accepted")
	}
}
