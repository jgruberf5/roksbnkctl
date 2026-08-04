package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/flpstatus"
	"github.com/spf13/cobra"
)

var flagFLPStatusURL string

var flpStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the F5 License Proxy's live status (services, listener, F5/TEEM, CNE fields)",
	Long: `Fetches /api/status from the FLP's status service (the flp-status container,
which runs alongside the proxy on the VSI pod or in the cluster) and renders it.

The service URL is derived from the workspace's flp-outputs.json (the FLP endpoint
host, port 80) unless --url is given. Output honors -o json.`,
	RunE: runFLPStatus,
}

func init() {
	flpStatusCmd.Flags().StringVar(&flagFLPStatusURL, "url", "", "flp-status base URL (default: derived from the FLP endpoint, port 80)")
	flpCmd.AddCommand(flpStatusCmd)
}

func runFLPStatus(cmd *cobra.Command, _ []string) error {
	base := flagFLPStatusURL
	if base == "" {
		out, err := config.ReadFLPOutputs(flagWorkspace)
		if err != nil {
			return fmt.Errorf("%w — or pass --url", err)
		}
		// Prefer the operator floating IP when one was attached — it's the address
		// reachable from a machine outside the VPC. Fall back to the (private) CWC
		// endpoint host, which works when run co-located (same VPC / over the TGW).
		host := out.FloatingIP
		if host == "" {
			host, err = hostOf(out.ExternalEndpoint)
			if err != nil {
				return fmt.Errorf("deriving status URL from FLP endpoint %q: %w", out.ExternalEndpoint, err)
			}
		}
		base = "http://" + host // status service is plain HTTP on :80
	}
	base = strings.TrimRight(base, "/")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(base + "/api/status")
	if err != nil {
		return fmt.Errorf("reaching flp-status at %s: %w (is the flp-status service up + reachable?)", base, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("flp-status returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	if flagOutput == "json" {
		fmt.Println(string(body))
		return nil
	}
	var s flpstatus.Status
	if err := json.Unmarshal(body, &s); err != nil {
		return fmt.Errorf("parsing status: %w", err)
	}
	renderFLPStatus(os.Stdout, s, base+"/")
	return nil
}

func hostOf(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	if u.Hostname() == "" {
		return "", fmt.Errorf("no host in %q", endpoint)
	}
	return u.Hostname(), nil
}

func dotFor(ind flpstatus.Indicator) string {
	c := map[flpstatus.Indicator]string{
		flpstatus.Up: "\033[32m", flpstatus.Down: "\033[31m",
		flpstatus.Pending: "\033[33m", flpstatus.Unknown: "\033[90m",
	}[ind]
	if flagNoColor || c == "" {
		return "[" + string(ind) + "]"
	}
	return c + "●\033[0m"
}

func renderFLPStatus(w io.Writer, s flpstatus.Status, webURL string) {
	fmt.Fprintf(w, "F5 License Proxy  (deployment: %s, checked %s)\n", s.Deployment, s.CheckedAt)
	fmt.Fprintf(w, "  web UI: %s\n\n", webURL)
	fmt.Fprintf(w, "  %s listener   %s", dotFor(s.Listener.Indicator), s.Listener.Endpoint)
	if s.Listener.HTTPCode > 0 {
		fmt.Fprintf(w, "  (HTTP %d)", s.Listener.HTTPCode)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %s F5 / TEEM  %s\n\n", dotFor(s.TEEM.Indicator), s.TEEM.Detail)
	fmt.Fprintln(w, "  dependent services:")
	for _, sv := range s.Services {
		fmt.Fprintf(w, "    %s %-18s %s\n", dotFor(sv.Indicator), sv.Name, sv.Detail)
	}
	fmt.Fprintln(w, "\n  CNEInstance / bnk.flp.external:")
	fmt.Fprintf(w, "    url:         %s\n", s.CNE.Endpoint)
	fmt.Fprintf(w, "    license_mode:%s\n", "  "+s.CNE.Mode)
	if s.CNE.RootCAB64 != "" {
		fmt.Fprintf(w, "    root_ca_b64: %s…(%d chars)\n", firstN(s.CNE.RootCAB64, 24), len(s.CNE.RootCAB64))
	}
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
