package forge

// accessmethod.go — register a jumphost as a forge SSH "access-method" record
// via POST /api/ssh-credentials.
//
// IMPORTANT LIMITATION (EICE / no static key):
//   The forge SSHCredential model stores a reusable private key that forge
//   uses to open SSH sessions.  awsbnkctl's jumphost is reached exclusively
//   via AWS EC2 Instance Connect Endpoint (EICE): each session mints an
//   ephemeral 60-second key — there is no long-lived private key we can
//   store.  Forge therefore CANNOT actually SSH to this jumphost using the
//   registered record; the record is informational only.
//   The private_key field in SSHCredentialCreate is optional (nullable), so
//   the POST succeeds with private_key=null.  forge will mark the credential's
//   last_test_status as "failed" if it ever tries to test it.
//
// See: bnk-forge-v2/backend/routes/ssh_credentials.py  SSHCredentialCreate
//      bnk-forge-v2/backend/models/ssh_credential.py   SSHCredential

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// SSHCredentialEndpoint is the forge REST path for SSH credential CRUD.
const SSHCredentialEndpoint = "/api/ssh-credentials"

// AccessMethodOptions carries all caller-supplied data for registering the
// jumphost as a forge SSH access-method record.
type AccessMethodOptions struct {
	// RestURL is the forge REST base URL (e.g. "http://localhost:8000").
	RestURL string
	// Creds are the forge REST login credentials.
	Creds RestCreds
	// Name is the record name in forge (e.g. "awsbnkctl-jumphost-my-cluster").
	// Required.
	Name string
	// Host is the jumphost EC2 instance-id or private IP stored in the record.
	// forge will not be able to reach it (EICE-only access), but the field is
	// required by the schema.
	Host string
	// Region is the AWS region — included in the description for
	// self-documentation.
	Region string
	// InstanceID is the EC2 instance-id — included in the description.
	InstanceID string
}

// AccessMethodResponse is the subset of SSHCredentialResponse fields that
// callers need.
type AccessMethodResponse struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Host string `json:"host"`
}

// RegisterJumphostAccessMethod POSTs the jumphost to forge's
// /api/ssh-credentials endpoint as a named SSH access-method record.
//
// Upsert semantics: if the name already exists (409 or found in list), the
// existing record is PUT-updated with the current description.
//
// The HTTP transport is the same benchmarkHTTPDoFn seam used by
// PushBenchmarkResult — tests can inject a mock without touching
// http.DefaultClient.
//
// NOTE: this registration is record-only.  Forge cannot actually SSH to an
// EICE-accessed jumphost (no reusable private key).  See package-level comment.
func RegisterJumphostAccessMethod(ctx context.Context, opts AccessMethodOptions) (AccessMethodResponse, error) {
	if opts.RestURL == "" {
		return AccessMethodResponse{}, fmt.Errorf("forge.RegisterJumphostAccessMethod: RestURL is required")
	}
	if opts.Name == "" {
		return AccessMethodResponse{}, fmt.Errorf("forge.RegisterJumphostAccessMethod: Name is required")
	}
	if opts.Host == "" {
		return AccessMethodResponse{}, fmt.Errorf("forge.RegisterJumphostAccessMethod: Host is required")
	}

	base := strings.TrimRight(opts.RestURL, "/")

	token, err := bmkRestLogin(ctx, base, opts.Creds.restUsername(), opts.Creds.restPassword())
	if err != nil {
		return AccessMethodResponse{}, fmt.Errorf("forge access-method: login: %w", err)
	}

	desc := fmt.Sprintf(
		"awsbnkctl-managed jumphost. Access is via AWS EC2 Instance Connect Endpoint (EICE) "+
			"with a 60-second ephemeral key — no static private key exists. "+
			"Region: %s  InstanceID: %s. "+
			"Forge cannot SSH to this host directly; record is informational.",
		opts.Region, opts.InstanceID,
	)

	body := map[string]any{
		"name":        opts.Name,
		"description": desc,
		"host":        opts.Host,
		"port":        22,
		"username":    "ec2-user",
		"auth_type":   "key",
		// private_key intentionally omitted (null): EICE uses ephemeral keys.
		// The forge schema accepts private_key=null for auth_type="key".
	}

	var created AccessMethodResponse
	err = bmkRestPost(ctx, base+SSHCredentialEndpoint, token, body, &created)
	if err == nil {
		return created, nil
	}

	// 409 = name conflict — fall back to list-and-update (mirror rest.go pattern).
	var herr *restHTTPErr
	if !errors.As(err, &herr) || herr.StatusCode != http.StatusConflict {
		return AccessMethodResponse{}, fmt.Errorf("forge access-method: create: %w", err)
	}

	existing, lookupErr := sshCredFindByName(ctx, base, token, opts.Name)
	if lookupErr != nil {
		return AccessMethodResponse{}, fmt.Errorf("forge access-method: 409 + list failed: %w (original: %v)", lookupErr, err)
	}

	updateBody := map[string]any{
		"description": desc,
		"host":        opts.Host,
	}
	updateURL := fmt.Sprintf("%s%s/%d", base, SSHCredentialEndpoint, existing.ID)
	var updated AccessMethodResponse
	if putErr := bmkRestPut(ctx, updateURL, token, updateBody, &updated); putErr != nil {
		return AccessMethodResponse{}, fmt.Errorf("forge access-method: PUT update: %w", putErr)
	}
	if updated.ID == 0 {
		updated = existing
	}
	return updated, nil
}

// sshCredFindByName GETs /api/ssh-credentials and returns the record whose
// name matches exactly.
func sshCredFindByName(ctx context.Context, base, token, name string) (AccessMethodResponse, error) {
	var list []AccessMethodResponse
	if err := bmkRestGet(ctx, base+SSHCredentialEndpoint, token, &list); err != nil {
		return AccessMethodResponse{}, fmt.Errorf("list ssh-credentials: %w", err)
	}
	for _, r := range list {
		if r.Name == name {
			return r, nil
		}
	}
	return AccessMethodResponse{}, fmt.Errorf("ssh-credential %q not found in forge", name)
}

// bmkRestGet and bmkRestPut are defined in benchmark.go — same file, same
// package — and share the benchmarkHTTPDoFn seam.
