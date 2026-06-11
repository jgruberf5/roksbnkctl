package tf

// PRD 16 — local → COS/S3 state migration. `roksbnkctl state migrate` opens
// each phase with the (now s3-configured) backend override and runs
// `terraform init -migrate-state` to copy the existing local state into the
// bucket. A remote-key HEAD check (RemoteS3StateExists) gates the
// force-copy so an occupied key is never silently overwritten.

import (
	"context"
	"fmt"
	"io"
	"os/exec"

	cosaws "github.com/IBM/ibm-cos-sdk-go/aws"
	"github.com/IBM/ibm-cos-sdk-go/aws/awserr"
	"github.com/IBM/ibm-cos-sdk-go/aws/credentials"
	"github.com/IBM/ibm-cos-sdk-go/aws/session"
	"github.com/IBM/ibm-cos-sdk-go/service/s3"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// InitMigrate runs `terraform init -migrate-state -force-copy -input=false`
// in the phase's source dir, copying the existing (local) state into the
// backend the override now points at (s3). Driven via exec (not tfexec —
// the pinned terraform-exec lacks a MigrateState init option) so the flags
// are explicit. The s3 backend's HMAC creds ride on the env Open already
// set (AWS_*). The caller must HEAD-check the remote key first; -force-copy
// suppresses terraform's migration prompt and would otherwise overwrite.
func (w *Workspace) InitMigrate(ctx context.Context, stdout, stderr io.Writer) error {
	tfBin, err := exec.LookPath("terraform")
	if err != nil {
		return fmt.Errorf("terraform not found on PATH: %w", err)
	}
	cmd := exec.CommandContext(ctx, tfBin, "init", "-migrate-state", "-force-copy", "-input=false")
	cmd.Dir = w.SourceDir()
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("terraform init -migrate-state: %w", err)
	}
	return nil
}

// RemoteS3StateExists reports whether the state object already exists in the
// COS bucket — the clobber check before a force-copy migration. Uses an
// HMAC-authed S3 client against the configured endpoint/region. A 404 /
// NotFound means the key is empty (safe to migrate).
func RemoteS3StateExists(ctx context.Context, s3cfg config.StateS3Cfg, key, access, secret string) (bool, error) {
	sess, err := session.NewSession(cosaws.NewConfig().
		WithRegion(s3cfg.Region).
		WithEndpoint(s3cfg.Endpoint).
		WithS3ForcePathStyle(true).
		WithCredentials(credentials.NewStaticCredentials(access, secret, "")))
	if err != nil {
		return false, fmt.Errorf("building COS S3 client: %w", err)
	}
	svc := s3.New(sess)
	_, err = svc.HeadObjectWithContext(ctx, &s3.HeadObjectInput{
		Bucket: cosaws.String(s3cfg.Bucket),
		Key:    cosaws.String(key),
	})
	if err == nil {
		return true, nil
	}
	if rf, ok := err.(awserr.RequestFailure); ok && rf.StatusCode() == 404 {
		return false, nil
	}
	if aerr, ok := err.(awserr.Error); ok {
		switch aerr.Code() {
		case "NotFound", s3.ErrCodeNoSuchKey:
			return false, nil
		}
	}
	return false, fmt.Errorf("checking remote state key %q: %w", key, err)
}
