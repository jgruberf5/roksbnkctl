// Package jumphost provides SSH-via-EICE curl probe utilities for the
// slice-12 multi-ENI jumphost. It is a pure leaf package — it must NOT
// import internal/cli or internal/scenarios. The CLI layer translates
// cobra flags into ProbeOptions at the boundary.
package jumphost

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// ProbeOptions configures a jumphost curl probe run.
type ProbeOptions struct {
	// Region is the AWS region (e.g. "ap-southeast-2").
	Region string
	// InstanceID is the jumphost EC2 instance ID (JUMPHOST_INSTANCE_ID).
	InstanceID string
	// SourceIP is the jumphost's BNK_EXT ENI IP (JUMPHOST_BNK_EXT_ENI_IP).
	// curl --interface uses this so traffic hits the real data path.
	SourceIP string
	// VIP is the Gateway VIP to curl.
	VIP string
	// Iterations is the number of curl probes. Default 5.
	Iterations int
	// Timeout is the per-curl timeout. Default 10s.
	Timeout time.Duration
	// User is the SSH user on the jumphost. Default "ec2-user".
	User string
}

// ProbeResult records the outcome of one curl iteration.
type ProbeResult struct {
	Iteration int     `json:"iteration"`
	HTTPCode  int     `json:"http_code"`
	Seconds   float64 `json:"seconds"`
	Err       string  `json:"error,omitempty"`
}

// RunCurlProbes mints an ephemeral SSH key, pushes it via EC2 Instance
// Connect, then runs opts.Iterations curls from the jumphost via the
// EICE tunnel. Returns per-iteration results.
//
// Shelling to `aws ec2-instance-connect open-tunnel` + `ssh` reproduces
// the slice-12 pattern operators already use. Requires the `aws` CLI and
// `ssh` to be on PATH.
func RunCurlProbes(ctx context.Context, opts ProbeOptions) ([]ProbeResult, error) {
	if opts.Iterations <= 0 {
		opts.Iterations = 5
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	if opts.User == "" {
		opts.User = "ec2-user"
	}

	priv, pubAuth, err := GenerateEphemeralED25519()
	if err != nil {
		return nil, fmt.Errorf("ephemeral key: %w", err)
	}

	keyFile, err := os.CreateTemp("", "awsbnkctl-jumphost-*.key")
	if err != nil {
		return nil, fmt.Errorf("temp key file: %w", err)
	}
	defer os.Remove(keyFile.Name())
	if _, err := keyFile.Write(priv); err != nil {
		_ = keyFile.Close()
		return nil, fmt.Errorf("write key: %w", err)
	}
	if err := keyFile.Chmod(0o600); err != nil {
		_ = keyFile.Close()
		return nil, fmt.Errorf("chmod key: %w", err)
	}
	if err := keyFile.Close(); err != nil {
		return nil, fmt.Errorf("close key: %w", err)
	}

	pubFile, err := os.CreateTemp("", "awsbnkctl-jumphost-*.pub")
	if err != nil {
		return nil, fmt.Errorf("temp pub file: %w", err)
	}
	defer os.Remove(pubFile.Name())
	if _, err := pubFile.WriteString(pubAuth); err != nil {
		_ = pubFile.Close()
		return nil, fmt.Errorf("write pub: %w", err)
	}
	if err := pubFile.Close(); err != nil {
		return nil, fmt.Errorf("close pub: %w", err)
	}

	if err := PushSSHPublicKey(ctx, opts.Region, opts.InstanceID, pubFile.Name()); err != nil {
		return nil, fmt.Errorf("send-ssh-public-key: %w", err)
	}

	// Allow the key a moment to settle (EC2 Instance Connect ~60s TTL).
	time.Sleep(2 * time.Second)

	results := make([]ProbeResult, 0, opts.Iterations)
	for i := 1; i <= opts.Iterations; i++ {
		code, secs, perr := SSHCurlViaEICE(ctx, opts.Region, opts.InstanceID, keyFile.Name(), opts.SourceIP, opts.VIP, opts.Timeout)
		res := ProbeResult{Iteration: i, HTTPCode: code, Seconds: secs}
		if perr != nil {
			res.Err = perr.Error()
		}
		results = append(results, res)
	}
	return results, nil
}

// PushSSHPublicKey sends a public key to the jumphost via EC2 Instance
// Connect (aws ec2-instance-connect send-ssh-public-key). pubKeyPath must
// point to a file whose contents begin with "ssh-ed25519 ...".
func PushSSHPublicKey(ctx context.Context, region, instanceID, pubKeyPath string) error {
	args := []string{
		"ec2-instance-connect", "send-ssh-public-key",
		"--instance-id", instanceID,
		"--instance-os-user", "ec2-user",
		"--ssh-public-key", "file://" + pubKeyPath,
		"--region", region,
	}
	// #nosec G204 -- args are constructed from validated state.env values
	// (instanceID, region) plus a temp pubkey path we just minted; no caller-
	// supplied free text reaches the subprocess.
	cmd := exec.CommandContext(ctx, "aws", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("aws %s: %w (stderr: %s)", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// SSHCurlViaEICE opens an EICE tunnel to the jumphost and runs a single
// curl --interface <sourceIP> http://<vip>/ with the given timeout.
// Returns the HTTP response code, elapsed seconds, and any error.
func SSHCurlViaEICE(ctx context.Context, region, instanceID, keyPath, sourceIP, vip string, timeout time.Duration) (int, float64, error) {
	proxy := fmt.Sprintf(`ProxyCommand=aws ec2-instance-connect open-tunnel --instance-id %s --region %s`, instanceID, region)
	remoteCmd := fmt.Sprintf(`curl -s -o /dev/null -w '%%{http_code} %%{time_total}' --interface %s --max-time %d http://%s/`,
		sourceIP, int(timeout.Seconds()), vip)
	args := []string{
		"-o", proxy,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=15",
		"-i", keyPath,
		"ec2-user@" + instanceID,
		remoteCmd,
	}
	// #nosec G204 -- args are constructed from validated state.env values
	// (instanceID, region, sourceIP) + a temp private-key path we just minted +
	// a VIP from cluster.yaml; no caller-supplied free text reaches the subprocess.
	cmd := exec.CommandContext(ctx, "ssh", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return 0, 0, fmt.Errorf("ssh-curl: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	// stdout format: "<code> <seconds>"
	out := strings.TrimSpace(stdout.String())
	parts := strings.Fields(out)
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("unexpected ssh-curl output %q", out)
	}
	var code int
	var secs float64
	if _, err := fmt.Sscanf(parts[0], "%d", &code); err != nil {
		return 0, 0, fmt.Errorf("parsing http_code %q: %w", parts[0], err)
	}
	if _, err := fmt.Sscanf(parts[1], "%f", &secs); err != nil {
		return code, 0, fmt.Errorf("parsing time_total %q: %w", parts[1], err)
	}
	return code, secs, nil
}

// GenerateEphemeralED25519 mints a fresh ED25519 keypair and returns
// (private-key in OpenSSH PEM, public-key in authorized_keys format, error).
func GenerateEphemeralED25519() (privPEM []byte, pubAuthLine string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	pemBlock, err := ssh.MarshalPrivateKey(priv, "awsbnkctl-jumphost-ephemeral")
	if err != nil {
		return nil, "", err
	}
	pemBytes := pem.EncodeToMemory(pemBlock)

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, "", err
	}
	auth := fmt.Sprintf("ssh-ed25519 %s awsbnkctl-jumphost-ephemeral\n",
		base64.StdEncoding.EncodeToString(sshPub.Marshal()))
	return pemBytes, auth, nil
}
