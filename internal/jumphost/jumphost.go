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
	// Hostname sets the HTTP Host header so requests match hostname-scoped
	// HTTPRoutes. Empty means no Host header (matches no-hostname routes).
	Hostname string
}

// ProbeResult records the outcome of one curl iteration.
type ProbeResult struct {
	Iteration int     `json:"iteration"`
	HTTPCode  int     `json:"http_code"`
	Seconds   float64 `json:"seconds"`
	Err       string  `json:"error,omitempty"`
}

// prepareEICEKey mints an ephemeral ED25519 keypair, writes it to temp files,
// pushes the public half via EC2 Instance Connect, waits 2 s for it to settle,
// and returns the private-key path plus a cleanup func that removes both files.
// The caller is responsible for calling cleanup() (typically via defer).
func prepareEICEKey(ctx context.Context, region, instanceID string) (keyPath string, cleanup func(), err error) {
	priv, pubAuth, err := GenerateEphemeralED25519()
	if err != nil {
		return "", func() {}, fmt.Errorf("ephemeral key: %w", err)
	}

	keyFile, err := os.CreateTemp("", "awsbnkctl-jumphost-*.key")
	if err != nil {
		return "", func() {}, fmt.Errorf("temp key file: %w", err)
	}
	if _, err := keyFile.Write(priv); err != nil {
		_ = keyFile.Close()
		_ = os.Remove(keyFile.Name())
		return "", func() {}, fmt.Errorf("write key: %w", err)
	}
	if err := keyFile.Chmod(0o600); err != nil {
		_ = keyFile.Close()
		_ = os.Remove(keyFile.Name())
		return "", func() {}, fmt.Errorf("chmod key: %w", err)
	}
	if err := keyFile.Close(); err != nil {
		_ = os.Remove(keyFile.Name())
		return "", func() {}, fmt.Errorf("close key: %w", err)
	}

	pubFile, err := os.CreateTemp("", "awsbnkctl-jumphost-*.pub")
	if err != nil {
		_ = os.Remove(keyFile.Name())
		return "", func() {}, fmt.Errorf("temp pub file: %w", err)
	}
	if _, err := pubFile.WriteString(pubAuth); err != nil {
		_ = pubFile.Close()
		_ = os.Remove(keyFile.Name())
		_ = os.Remove(pubFile.Name())
		return "", func() {}, fmt.Errorf("write pub: %w", err)
	}
	if err := pubFile.Close(); err != nil {
		_ = os.Remove(keyFile.Name())
		_ = os.Remove(pubFile.Name())
		return "", func() {}, fmt.Errorf("close pub: %w", err)
	}

	cleanupFn := func() {
		_ = os.Remove(keyFile.Name())
		_ = os.Remove(pubFile.Name())
	}

	if err := PushSSHPublicKey(ctx, region, instanceID, pubFile.Name()); err != nil {
		cleanupFn()
		return "", func() {}, fmt.Errorf("send-ssh-public-key: %w", err)
	}

	// Allow the key a moment to settle (EC2 Instance Connect ~60s TTL).
	time.Sleep(2 * time.Second)

	return keyFile.Name(), cleanupFn, nil
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

	keyPath, cleanup, err := prepareEICEKey(ctx, opts.Region, opts.InstanceID)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	results := make([]ProbeResult, 0, opts.Iterations)
	for i := 1; i <= opts.Iterations; i++ {
		code, secs, perr := SSHCurlViaEICE(ctx, opts.Region, opts.InstanceID, keyPath, opts.SourceIP, opts.VIP, opts.Hostname, opts.Timeout)
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

// buildCurlCmd returns the remote curl command. When host is non-empty it
// adds -H 'Host: <host>' so requests match hostname-scoped HTTPRoutes.
func buildCurlCmd(sourceIP, vip, host string, timeoutSecs int) string {
	hostHdr := ""
	if host != "" {
		hostHdr = fmt.Sprintf(`-H 'Host: %s' `, host)
	}
	return fmt.Sprintf(`curl -s -o /dev/null -w '%%{http_code} %%{time_total}' %s--interface %s --max-time %d http://%s/`,
		hostHdr, sourceIP, timeoutSecs, vip)
}

// SSHCurlViaEICE opens an EICE tunnel to the jumphost and runs a single
// curl --interface <sourceIP> http://<vip>/ with the given timeout.
// When host is non-empty, -H 'Host: <host>' is added so requests match
// hostname-scoped HTTPRoutes.
// Returns the HTTP response code, elapsed seconds, and any error.
func SSHCurlViaEICE(ctx context.Context, region, instanceID, keyPath, sourceIP, vip, host string, timeout time.Duration) (int, float64, error) {
	proxy := fmt.Sprintf(`ProxyCommand=aws ec2-instance-connect open-tunnel --instance-id %s --region %s`, instanceID, region)
	remoteCmd := buildCurlCmd(sourceIP, vip, host, int(timeout.Seconds()))
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

// BodyProbeResult records the outcome of one body-capturing curl iteration.
type BodyProbeResult struct {
	Iteration int    `json:"iteration"`
	HTTPCode  int    `json:"http_code"`
	Body      string `json:"body,omitempty"`
	Err       string `json:"error,omitempty"`
}

// buildCurlBodyCmd returns the remote curl command that captures the response
// body. Unlike buildCurlCmd it does NOT use -o /dev/null; it writes the body
// to stdout and appends the HTTP code on a final newline-separated line:
//
//	<body>\n<code>
//
// When host is non-empty, -H 'Host: <host>' is added so requests match
// hostname-scoped HTTPRoutes.
func buildCurlBodyCmd(sourceIP, vip, host string, timeoutSecs int) string {
	hostHdr := ""
	if host != "" {
		hostHdr = fmt.Sprintf(`-H 'Host: %s' `, host)
	}
	return fmt.Sprintf(`curl -s -w '\n%%{http_code}' %s--interface %s --max-time %d http://%s/`,
		hostHdr, sourceIP, timeoutSecs, vip)
}

// SSHCurlBodyViaEICE opens an EICE tunnel to the jumphost and runs a single
// body-capturing curl. It returns the response body (trimmed), the HTTP code,
// and any error. The caller is responsible for minting + pushing the key before
// calling this (use prepareEICEKey).
func SSHCurlBodyViaEICE(ctx context.Context, region, instanceID, keyPath, sourceIP, vip, host string, timeout time.Duration) (body string, code int, err error) {
	proxy := fmt.Sprintf(`ProxyCommand=aws ec2-instance-connect open-tunnel --instance-id %s --region %s`, instanceID, region)
	remoteCmd := buildCurlBodyCmd(sourceIP, vip, host, int(timeout.Seconds()))
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
		return "", 0, fmt.Errorf("ssh-curl-body: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	// stdout format: "<body>\n<code>"
	// Split on the LAST newline — body may itself contain newlines.
	raw := stdout.String()
	lastNL := strings.LastIndex(raw, "\n")
	if lastNL < 0 {
		return "", 0, fmt.Errorf("unexpected ssh-curl-body output %q", raw)
	}
	bodyStr := strings.TrimSpace(raw[:lastNL])
	codeStr := strings.TrimSpace(raw[lastNL+1:])
	var httpCode int
	if _, parseErr := fmt.Sscanf(codeStr, "%d", &httpCode); parseErr != nil {
		return "", 0, fmt.Errorf("parsing http_code %q: %w", codeStr, parseErr)
	}
	return bodyStr, httpCode, nil
}

// RunCurlBodyProbes mints an ephemeral SSH key, pushes it via EC2 Instance
// Connect, then runs opts.Iterations body-capturing curls from the jumphost.
// The Host header is set from opts.Hostname (empty = no header).
// Returns per-iteration results including the response body.
func RunCurlBodyProbes(ctx context.Context, opts ProbeOptions) ([]BodyProbeResult, error) {
	if opts.Iterations <= 0 {
		opts.Iterations = 5
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	if opts.User == "" {
		opts.User = "ec2-user"
	}

	keyPath, cleanup, err := prepareEICEKey(ctx, opts.Region, opts.InstanceID)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	results := make([]BodyProbeResult, 0, opts.Iterations)
	for i := 1; i <= opts.Iterations; i++ {
		body, code, perr := SSHCurlBodyViaEICE(ctx, opts.Region, opts.InstanceID, keyPath, opts.SourceIP, opts.VIP, opts.Hostname, opts.Timeout)
		res := BodyProbeResult{Iteration: i, HTTPCode: code, Body: body}
		if perr != nil {
			res.Err = perr.Error()
		}
		results = append(results, res)
	}
	return results, nil
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
