//go:build integration

// PRD 16 — S3/COS state backend integration tests. Gated behind the
// `integration` build tag (so `go test ./...` stays offline) and skipped
// unless an S3-compatible endpoint is configured via env. CI runs these
// against a MinIO service (see .github/workflows/state-backend-it.yml):
//
//	ROKSBNKCTL_IT_S3_ENDPOINT, ROKSBNKCTL_IT_S3_BUCKET, ROKSBNKCTL_IT_S3_REGION,
//	AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY   (the HMAC creds)
//
//	go test -tags integration ./internal/tf/ -run IT_S3
//
// They exercise the REAL render path (backendOverrideHCL) + the REAL
// remote-key check (RemoteS3StateExists), so the backend HCL and the
// migration can't silently drift from what ships.

package tf

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

func itS3(t *testing.T) (config.StateCfg, string, string) {
	t.Helper()
	endpoint, bucket := os.Getenv("ROKSBNKCTL_IT_S3_ENDPOINT"), os.Getenv("ROKSBNKCTL_IT_S3_BUCKET")
	access, secret := os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY")
	if endpoint == "" || bucket == "" || access == "" || secret == "" {
		t.Skip("set ROKSBNKCTL_IT_S3_ENDPOINT + ROKSBNKCTL_IT_S3_BUCKET + AWS_* to run")
	}
	region := os.Getenv("ROKSBNKCTL_IT_S3_REGION")
	if region == "" {
		region = "us-east-1"
	}
	return config.StateCfg{Backend: "s3", S3: &config.StateS3Cfg{Endpoint: endpoint, Bucket: bucket, Region: region}}, access, secret
}

const itMainTF = `terraform {
  required_providers {
    null = { source = "hashicorp/null" }
  }
}
resource "null_resource" "x" {}
`

func itWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func itTerraform(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("terraform", args...)
	cmd.Dir = dir
	cmd.Env = os.Environ() // AWS_* inherited
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("terraform %v:\n%s", args, out)
	}
}

// Round-trip: apply → state lands at the per-phase key in the bucket, no
// local tfstate, and a re-plan reads the remote.
func TestIT_S3Backend_RoundTrip(t *testing.T) {
	st, access, secret := itS3(t)
	ctx := context.Background()
	dir := t.TempDir()

	body, useS3, err := backendOverrideHCL(st, "itws", "/itws/state")
	if err != nil || !useS3 {
		t.Fatalf("render s3 backend: %v useS3=%v", err, useS3)
	}
	key, _ := S3StateKey(st, "itws", "/itws/state")
	if exists, _ := RemoteS3StateExists(ctx, *st.S3, key, access, secret); exists {
		t.Fatalf("key %s already occupied — clean the bucket", key)
	}

	itWrite(t, filepath.Join(dir, "main.tf"), itMainTF)
	itWrite(t, filepath.Join(dir, "roksbnkctl_backend_override.tf"), body)
	itTerraform(t, dir, "init", "-input=false")
	itTerraform(t, dir, "apply", "-auto-approve", "-input=false")

	exists, herr := RemoteS3StateExists(ctx, *st.S3, key, access, secret)
	if herr != nil || !exists {
		t.Fatalf("state not in bucket after apply: exists=%v err=%v", exists, herr)
	}
	if _, err := os.Stat(filepath.Join(dir, "terraform.tfstate")); !os.IsNotExist(err) {
		t.Errorf("the s3 backend should leave NO local terraform.tfstate")
	}
	// -detailed-exitcode: 0 = no changes (remote state read cleanly).
	itTerraform(t, dir, "plan", "-input=false", "-detailed-exitcode")
}

// Migration: a local-state config flipped to s3 + `init -migrate-state`
// lands the existing state in the bucket (the `state migrate` mechanism).
func TestIT_S3Backend_Migrate(t *testing.T) {
	st, access, secret := itS3(t)
	ctx := context.Background()
	dir := t.TempDir()

	localBody, _, _ := backendOverrideHCL(config.StateCfg{Backend: "local"}, "mig", dir)
	itWrite(t, filepath.Join(dir, "main.tf"), itMainTF)
	itWrite(t, filepath.Join(dir, "roksbnkctl_backend_override.tf"), localBody)
	itTerraform(t, dir, "init", "-input=false")
	itTerraform(t, dir, "apply", "-auto-approve", "-input=false")
	if _, err := os.Stat(filepath.Join(dir, "terraform.tfstate")); err != nil {
		t.Fatalf("expected local state after apply: %v", err)
	}

	key, _ := S3StateKey(st, "mig", dir)
	if exists, _ := RemoteS3StateExists(ctx, *st.S3, key, access, secret); exists {
		t.Fatalf("migrate target key %s already occupied — clean the bucket", key)
	}

	s3Body, _, _ := backendOverrideHCL(st, "mig", dir)
	itWrite(t, filepath.Join(dir, "roksbnkctl_backend_override.tf"), s3Body)
	itTerraform(t, dir, "init", "-migrate-state", "-force-copy", "-input=false")

	exists, herr := RemoteS3StateExists(ctx, *st.S3, key, access, secret)
	if herr != nil || !exists {
		t.Fatalf("migration didn't land state in the bucket: exists=%v err=%v", exists, herr)
	}
}
