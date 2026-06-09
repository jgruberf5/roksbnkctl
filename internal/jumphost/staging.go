package jumphost

import (
	"context"
	"encoding/base64"
	"fmt"
	"path/filepath"
)

// Package-level function-variable seams consumed ONLY by RunStagingCommands and
// CopyFileViaEICE. Default to the real implementations. Tests override these via
// export_test.go to assert command sequencing without network.
//
// Do NOT route RunCurlProbes / StartHTTPResponder / etc. through these seams —
// those paths are left byte-for-byte untouched.
var (
	prepareEICEKeyFn   = prepareEICEKey
	sshRunViaEICEFn    = SSHRunViaEICE
	pushSSHPublicKeyFn = PushSSHPublicKey
)

// RunStagingCommands mints+pushes an ephemeral EICE key internally (mirroring
// StartHTTPResponder), then runs each command in order over SSH-via-EICE,
// re-pushing the public key before EACH command to survive the ~60s EICE TTL.
//
// Returns a per-command stdout slice (one entry per command actually attempted)
// and the first error encountered (fail-fast: stops on the first failing command).
// On error, len(out) < len(commands); the partial stdout is returned so the caller
// can log the remote output of the failing step.
//
// RunStagingCommands is a pure leaf — it imports no cli/scenarios/intent/demo/state
// packages. All embedding and state reads happen in the calling phase.
func RunStagingCommands(ctx context.Context, opts ProbeOptions, commands []string) ([]string, error) {
	keyPath, pubKeyPath, cleanup, err := prepareEICEKeyFn(ctx, opts.Region, opts.InstanceID)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	out := make([]string, 0, len(commands))
	for _, cmd := range commands {
		// Re-push before every command to reset the ~60s EICE TTL.
		// Mirror RunCurlProbes ~145: errors are intentionally ignored (best-effort
		// TTL extension; the subsequent SSH call surfaces the real failure).
		_ = pushSSHPublicKeyFn(ctx, opts.Region, opts.InstanceID, pubKeyPath)

		stdout, runErr := sshRunViaEICEFn(ctx, opts.Region, opts.InstanceID, keyPath, cmd)
		out = append(out, stdout)
		if runErr != nil {
			// Fail-fast: return stdout-so-far + error so the phase can log the
			// remote output of the failing step (mirrors StartHTTPResponder ~410).
			return out, runErr
		}
	}
	return out, nil
}

// CopyFileViaEICE mints+pushes an ephemeral key, then writes content to remotePath
// on the jumphost by base64-encoding content and piping it through SSH where it is
// decoded back to bytes. Overwrites remotePath unconditionally (idempotent re-run =
// clean overwrite; no skip-if-present needed for small files). Re-pushes the key
// once before the write (single command, so no per-step loop needed).
//
// The base64 mechanism is binary-safe for arbitrary bytes including Python source
// files — unlike cat-heredoc which breaks on embedded sentinel strings.
func CopyFileViaEICE(ctx context.Context, opts ProbeOptions, content []byte, remotePath string) error {
	keyPath, pubKeyPath, cleanup, err := prepareEICEKeyFn(ctx, opts.Region, opts.InstanceID)
	if err != nil {
		return err
	}
	defer cleanup()

	// Re-push before the write to reset the EICE TTL.
	_ = pushSSHPublicKeyFn(ctx, opts.Region, opts.InstanceID, pubKeyPath)

	b64 := base64.StdEncoding.EncodeToString(content)
	dir := filepath.Dir(remotePath)

	// shellSingleQuote on the base64 string is a no-op (no single quotes in base64
	// alphabet) but documents intent and is defensive per the architect review.
	remoteCmd := fmt.Sprintf(
		"mkdir -p %s && printf '%%s' %s | base64 -d > %s",
		shellSingleQuote(dir),
		shellSingleQuote(b64),
		shellSingleQuote(remotePath),
	)

	_, runErr := sshRunViaEICEFn(ctx, opts.Region, opts.InstanceID, keyPath, remoteCmd)
	if runErr != nil {
		return fmt.Errorf("copy %s: %w", remotePath, runErr)
	}
	return nil
}

// ShellSingleQuote wraps s in single quotes safe for /bin/sh, escaping any
// embedded single quotes via the close-quote/escaped-quote/reopen-quote idiom.
//
// Exported for callers (e.g. the BIG-IP onboarding phase, F2-B2) that compose
// nested remote shell commands — an `ssh ... admin@bigip '<cmd>'` run ON the
// jumphost — and must single-quote the inner BIG-IP command safely. Thin wrapper
// over the package-internal shellSingleQuote so the internal helper stays
// untouched.
func ShellSingleQuote(s string) string {
	return shellSingleQuote(s)
}

// GrpcurlInstallCmd returns the idempotent remote shell command that installs
// grpcurl v1.9.3 to /usr/local/bin on an AL2023 x86_64 jumphost.
//
// The command:
//  1. Skips the download if grpcurl is already on PATH (skip-if-present).
//  2. Downloads the pinned v1.9.3 linux x86_64 release tarball from GitHub.
//  3. Extracts grpcurl directly to /usr/local/bin via sudo tar (avoids a temp dir).
//  4. Prints the installed version to confirm success.
//
// Exported so the phase can call it directly; also used in staging_test.go for
// string-level assertions (same pattern as the buildCurlCmd family).
func GrpcurlInstallCmd() string {
	// Pinned release URL: grpcurl v1.9.3, linux x86_64, AL2023 EKS jumphost.
	const releaseURL = "https://github.com/fullstorydev/grpcurl/releases/download/v1.9.3/grpcurl_1.9.3_linux_x86_64.tar.gz"
	return fmt.Sprintf(
		`command -v grpcurl >/dev/null 2>&1 || (curl -sSL %s | sudo tar -xz -C /usr/local/bin grpcurl); grpcurl --version`,
		releaseURL,
	)
}
