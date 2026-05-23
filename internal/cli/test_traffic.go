package cli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

var (
	flagTrafficConfig     string
	flagTrafficVIP        string
	flagTrafficIterations int
	flagTrafficTimeout    time.Duration
)

var testTrafficCmd = &cobra.Command{
	Use:   "traffic",
	Short: "Drive HTTP traffic through TMM from the slice-12 jumphost (simple-ingress scenario)",
	Long: `awsbnkctl test traffic exercises the BNK data plane end-to-end:

  1. Reads JUMPHOST_* from state.env (provisioned by Phase 17b — slice-12).
  2. Pushes an ephemeral SSH key to the jumphost via EC2 Instance Connect.
  3. Opens an EICE tunnel + SSHes into the jumphost.
  4. Curls --interface <JUMPHOST_BNK_EXT_ENI_IP> http://<VIP>/ N times and
     reports the HTTP code distribution.

This is the first concrete ` + "`awsbnkctl test traffic`" + ` scenario; the full
scenarios framework (PRD-09 / slice-13) generalises this to arbitrary
named scenarios with manifests, applies, verifies, and an ASCII env diagram.
For now: this verb is the simple-ingress scenario hard-coded.

If the BNK pool member is stale (HTTP 500 on the first run), run
` + "`awsbnkctl bnk resync <httproute> -n <ns>`" + ` first (slice-11b) and re-run.
A follow-up will fold that auto-call in once slice-11b merges to main.

Exit 0 when every probe returns the expected status (default 200), non-zero
on any miss.`,
	RunE: runTestTrafficCmd,
}

func init() {
	testTrafficCmd.Flags().StringVar(&flagTrafficConfig, "config", "", "path to cluster.yaml (required; state.env path is derived from it)")
	testTrafficCmd.Flags().StringVar(&flagTrafficVIP, "vip", "", "Gateway VIP to curl (default: <BNK_EXT_CIDR>.100 derived from cluster.yaml)")
	testTrafficCmd.Flags().IntVar(&flagTrafficIterations, "iterations", 5, "number of curl iterations against the VIP")
	testTrafficCmd.Flags().DurationVar(&flagTrafficTimeout, "timeout", 10*time.Second, "per-curl timeout")

	_ = testTrafficCmd.MarkFlagRequired("config")
	testCmd.AddCommand(testTrafficCmd)
}

type trafficProbeResult struct {
	Iteration int     `json:"iteration"`
	HTTPCode  int     `json:"http_code"`
	Seconds   float64 `json:"seconds"`
	Err       string  `json:"error,omitempty"`
}

type trafficReport struct {
	Schema     string               `json:"schema"`
	Timestamp  time.Time            `json:"timestamp"`
	Cluster    string               `json:"cluster"`
	VIP        string               `json:"vip"`
	JumphostID string               `json:"jumphost_instance_id"`
	SourceIP   string               `json:"source_ip"`
	Iterations int                  `json:"iterations"`
	Probes     []trafficProbeResult `json:"probes"`
	PassCount  int                  `json:"pass_count"`
	FailCount  int                  `json:"fail_count"`
}

const trafficSchema = "awsbnkctl.traffic.v1"

func runTestTrafficCmd(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	cl, err := intent.Load(flagTrafficConfig)
	if err != nil {
		return fmt.Errorf("loading cluster.yaml: %w", err)
	}
	st, err := state.Load(cl.StateDir())
	if err != nil {
		return fmt.Errorf("loading state.env: %w", err)
	}

	instanceID := st.Get("JUMPHOST_INSTANCE_ID")
	sourceIP := st.Get("JUMPHOST_BNK_EXT_ENI_IP")
	if instanceID == "" || sourceIP == "" {
		return fmt.Errorf("jumphost not provisioned (JUMPHOST_INSTANCE_ID / JUMPHOST_BNK_EXT_ENI_IP missing from %s/state.env). "+
			"Enable testing.jumphost.enabled in cluster.yaml and run `awsbnkctl up` first", cl.StateDir())
	}

	vip := flagTrafficVIP
	if vip == "" {
		vip, err = deriveDefaultVIP(cl)
		if err != nil {
			return fmt.Errorf("deriving default VIP: %w (pass --vip)", err)
		}
	}

	if flagTestDryRun {
		fmt.Fprintf(os.Stderr, "→ traffic dry-run plan:\n")
		fmt.Fprintf(os.Stderr, "  cluster:       %s\n", cl.Metadata.Name)
		fmt.Fprintf(os.Stderr, "  jumphost:      %s (source-ip %s)\n", instanceID, sourceIP)
		fmt.Fprintf(os.Stderr, "  vip:           %s\n", vip)
		fmt.Fprintf(os.Stderr, "  curl:          %d × curl --interface %s --max-time %s http://%s/\n", flagTrafficIterations, sourceIP, flagTrafficTimeout, vip)
		return nil
	}

	// Mint an ephemeral SSH key + push it via EC2 Instance Connect, then
	// run `iterations` curls from the jumphost via the EICE tunnel.
	probes, err := runJumphostCurlProbes(ctx, cl.Metadata.Region, instanceID, sourceIP, vip, flagTrafficIterations, flagTrafficTimeout)
	if err != nil {
		return fmt.Errorf("jumphost curl probes: %w", err)
	}

	report := trafficReport{
		Schema:     trafficSchema,
		Timestamp:  time.Now().UTC(),
		Cluster:    cl.Metadata.Name,
		VIP:        vip,
		JumphostID: instanceID,
		SourceIP:   sourceIP,
		Iterations: flagTrafficIterations,
		Probes:     probes,
	}
	for _, p := range probes {
		if p.HTTPCode == 200 && p.Err == "" {
			report.PassCount++
		} else {
			report.FailCount++
		}
	}

	if flagOutput == "json" {
		if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
			return err
		}
	} else {
		printTrafficReportText(os.Stderr, report)
	}

	if report.FailCount > 0 {
		os.Exit(1)
	}
	return nil
}

func printTrafficReportText(w io.Writer, r trafficReport) {
	fmt.Fprintf(w, "\n## test traffic — %s\n", r.Cluster)
	fmt.Fprintf(w, "  jumphost:  %s  (source-ip %s)\n", r.JumphostID, r.SourceIP)
	fmt.Fprintf(w, "  vip:       %s\n", r.VIP)
	for _, p := range r.Probes {
		sym := "✓"
		if p.HTTPCode != 200 || p.Err != "" {
			sym = "✗"
		}
		if p.Err != "" {
			fmt.Fprintf(w, "  %s iter=%d  err=%s\n", sym, p.Iteration, p.Err)
		} else {
			fmt.Fprintf(w, "  %s iter=%d  HTTP=%d  time=%.3fs\n", sym, p.Iteration, p.HTTPCode, p.Seconds)
		}
	}
	verdict := "PASS"
	if r.FailCount > 0 {
		verdict = "FAIL"
	}
	fmt.Fprintf(w, "→ %s (%d/%d HTTP 200)\n", verdict, r.PassCount, r.Iterations)
}

// deriveDefaultVIP picks <BNK_EXT_CIDR network address>.100 — the
// convention used by examples/syd-tracer/cluster.yaml. Returns an error
// when the dataPath block is missing.
func deriveDefaultVIP(cl *intent.Cluster) (string, error) {
	if cl.Network.DataPath == nil || cl.Network.DataPath.External.CIDR == "" {
		return "", errors.New("network.dataPath.external.cidr not set; pass --vip explicitly")
	}
	cidr := cl.Network.DataPath.External.CIDR
	// e.g. "10.0.10.0/24" → "10.0.10.100"
	slash := strings.IndexByte(cidr, '/')
	if slash <= 0 {
		return "", fmt.Errorf("malformed dataPath.externalCIDR %q", cidr)
	}
	network := cidr[:slash]
	parts := strings.Split(network, ".")
	if len(parts) != 4 {
		return "", fmt.Errorf("non-IPv4 dataPath.externalCIDR %q", cidr)
	}
	parts[3] = "100"
	return strings.Join(parts, "."), nil
}

// runJumphostCurlProbes mints an ephemeral SSH key, pushes it via EC2
// Instance Connect, then runs `iterations` curls from the jumphost via
// the EICE tunnel (shelling to `aws ec2-instance-connect open-tunnel`
// + `ssh`). Returns per-iteration results.
//
// Shelling out (rather than using golang.org/x/crypto/ssh + a direct
// WebSocket EICE client) keeps this slice thin while reproducing the
// pattern operators already use by hand. The proper Go-native impl is
// a follow-up in the slice-13 scenarios runner.
func runJumphostCurlProbes(ctx context.Context, region, instanceID, sourceIP, vip string, iterations int, timeout time.Duration) ([]trafficProbeResult, error) {
	priv, pubAuth, err := generateEphemeralED25519()
	if err != nil {
		return nil, fmt.Errorf("ephemeral key: %w", err)
	}
	keyFile, err := os.CreateTemp("", "awsbnkctl-traffic-*.key")
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

	pubFile, err := os.CreateTemp("", "awsbnkctl-traffic-*.pub")
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

	if err := pushSSHPublicKey(ctx, region, instanceID, pubFile.Name()); err != nil {
		return nil, fmt.Errorf("send-ssh-public-key: %w", err)
	}

	// Allow the key a moment to settle (EC2 Instance Connect ~60s TTL is
	// fine; this just smooths the immediate SSH handshake).
	time.Sleep(2 * time.Second)

	results := make([]trafficProbeResult, 0, iterations)
	for i := 1; i <= iterations; i++ {
		code, secs, perr := sshCurlViaEICE(ctx, region, instanceID, keyFile.Name(), sourceIP, vip, timeout)
		res := trafficProbeResult{Iteration: i, HTTPCode: code, Seconds: secs}
		if perr != nil {
			res.Err = perr.Error()
		}
		results = append(results, res)
	}
	return results, nil
}

func pushSSHPublicKey(ctx context.Context, region, instanceID, pubKeyPath string) error {
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

func sshCurlViaEICE(ctx context.Context, region, instanceID, keyPath, sourceIP, vip string, timeout time.Duration) (int, float64, error) {
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

// generateEphemeralED25519 mints a fresh ED25519 keypair and returns
// (private-key in OpenSSH PEM, public-key in authorized_keys "ssh-ed25519 …" form).
func generateEphemeralED25519() ([]byte, string, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	pemBlock, err := ssh.MarshalPrivateKey(priv, "awsbnkctl-traffic-ephemeral")
	if err != nil {
		return nil, "", err
	}
	pemBytes := pem.EncodeToMemory(pemBlock)

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, "", err
	}
	auth := fmt.Sprintf("ssh-ed25519 %s awsbnkctl-traffic-ephemeral\n",
		base64.StdEncoding.EncodeToString(sshPub.Marshal()))
	return pemBytes, auth, nil
}
