// Package flpstatus gathers and serves the runtime status of an F5 License Proxy
// (FLP) — the same four-container stack whether it runs as a podman pod on a
// standalone VSI or as the helm chart in a ROKS cluster. It backs both the
// on-appliance web UI (cmd/flp-status) and `roksbnkctl flp status` (the CLI
// fetches /api/status from the same service). It has NO authentication by design:
// the FLP is a private endpoint and this is read-only proxy status.
package flpstatus

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// The FLP-dependent services, in start-order. Every one gets a status indicator.
var Services = []string{"postgresql", "vault", "vault-init", "f5-license-proxy"}

// Indicator is a coarse health state the UI renders as a colored dot.
type Indicator string

const (
	Up      Indicator = "up"      // green
	Down    Indicator = "down"    // red
	Pending Indicator = "pending" // amber (starting / not-yet-ready)
	Unknown Indicator = "unknown" // grey
)

// ServiceStatus is one FLP-dependent service (a container).
type ServiceStatus struct {
	Name      string    `json:"name"`
	Indicator Indicator `json:"indicator"`
	Detail    string    `json:"detail"`
}

// Status is the full snapshot the /api/status endpoint returns and the UI/CLI render.
type Status struct {
	Deployment string          `json:"deployment"` // "vsi" | "cluster"
	Services   []ServiceStatus `json:"services"`
	Listener   struct {
		Indicator Indicator `json:"indicator"`
		Endpoint  string    `json:"endpoint"` // https://<ip>:8443
		HTTPCode  int       `json:"http_code"`
	} `json:"listener"`
	TEEM struct {
		Indicator Indicator `json:"indicator"`
		Detail    string    `json:"detail"`
	} `json:"teem"`
	// CNE carries everything the CNEInstance CR (bnk.flp.external) needs to use this FLP.
	CNE struct {
		Endpoint  string `json:"endpoint"`
		RootCAB64 string `json:"root_ca_b64"`
		Mode      string `json:"mode"`
	} `json:"cne"`
	CheckedAt string `json:"checked_at"`
}

// Backend abstracts WHERE the FLP runs. The web layer is identical for both.
type Backend interface {
	Kind() string // "vsi" | "cluster"
	// Container returns the running/exited state + a one-line detail for a service.
	Container(ctx context.Context, name string) (Indicator, string)
	// ProxyURL is the base URL to probe the :8443 listener (localhost on the VSI,
	// the Service DNS in-cluster).
	ProxyURL() string
	// ProxyLog returns the last n log lines of the f5-license-proxy container.
	ProxyLog(ctx context.Context, n int) (string, error)
	// StreamProxyLog writes `podman/kubectl logs -f` lines to w until ctx is done.
	StreamProxyLog(ctx context.Context, w LineWriter)
}

// LineWriter receives streamed log lines (the SSE handler implements it).
type LineWriter interface {
	WriteLine(string)
}

// NewBackend selects the adapter. FLP_BACKEND overrides; otherwise auto-detect
// (a k8s service-account token present ⇒ cluster, else podman on the VSI).
func NewBackend() Backend {
	switch strings.ToLower(os.Getenv("FLP_BACKEND")) {
	case "podman", "vsi":
		return &podmanBackend{}
	case "k8s", "cluster":
		return &k8sBackend{ns: envOr("FLP_NAMESPACE", "f5-license-proxy")}
	}
	if _, err := os.Stat("/var/run/secrets/kubernetes.io/serviceaccount/token"); err == nil {
		return &k8sBackend{ns: envOr("FLP_NAMESPACE", "f5-license-proxy")}
	}
	return &podmanBackend{}
}

// Gather builds a full Status snapshot from a backend.
func Gather(ctx context.Context, b Backend) Status {
	var s Status
	s.Deployment = b.Kind()
	s.CheckedAt = nowRFC3339()

	for _, name := range Services {
		ind, detail := b.Container(ctx, name)
		s.Services = append(s.Services, ServiceStatus{Name: name, Indicator: ind, Detail: detail})
	}

	// Listener probe.
	code := probeHTTP(ctx, b.ProxyURL())
	s.Listener.Endpoint = b.ProxyURL()
	s.Listener.HTTPCode = code
	if code > 0 {
		s.Listener.Indicator = Up
	} else {
		s.Listener.Indicator = Down
	}

	// TEEM / F5 entitlement connection — inferred from the proxy log + a serving
	// listener. A proxy that is serving AND whose log shows no recent F5-connection
	// error is treated as connected; explicit error markers flip it Down.
	tail, _ := b.ProxyLog(ctx, 200)
	s.TEEM.Indicator, s.TEEM.Detail = teemState(tail, s.Listener.Indicator == Up)

	// CNE CR fields.
	s.CNE.Endpoint = firstNonEmpty(os.Getenv("FLP_ENDPOINT"), b.ProxyURL())
	s.CNE.Mode = "f5licenseproxy"
	s.CNE.RootCAB64 = rootCAB64()

	return s
}

// teemState scans the f5-license-proxy structured JSON log for F5/TEEM/entitlement
// connection markers. Conservative: a serving proxy with no recent connection
// error is "connected"; an explicit failure marker is "down".
func teemState(log string, serving bool) (Indicator, string) {
	low := strings.ToLower(log)
	// Look bottom-up for the most recent signal.
	lines := strings.Split(strings.TrimSpace(log), "\n")
	for i := len(lines) - 1; i >= 0 && i > len(lines)-60; i-- {
		l := strings.ToLower(lines[i])
		if strings.Contains(l, "entitlement") || strings.Contains(l, "teem") || strings.Contains(l, "product-s.apis.f5.com") || strings.Contains(l, "license") {
			if strings.Contains(l, "\"l\"=\"error\"") || strings.Contains(l, "failed") || strings.Contains(l, "unreachable") || strings.Contains(l, "timeout") {
				return Down, squish(lines[i])
			}
			if strings.Contains(l, "success") || strings.Contains(l, "connected") || strings.Contains(l, "retrieved") || strings.Contains(l, "\"l\"=\"info\"") {
				return Up, squish(lines[i])
			}
		}
	}
	if serving {
		return Up, "proxy serving; no recent F5 connection error in log"
	}
	if strings.Contains(low, "error") {
		return Down, "proxy not serving; errors present in log"
	}
	return Pending, "starting up"
}

// rootCAB64 returns the FLP's root CA (what bnk.flp.external.root_ca_b64 needs),
// base64-encoded. Reads the terraform-injected CA on the VSI, else FLP_ROOT_CA_B64.
func rootCAB64() string {
	if v := os.Getenv("FLP_ROOT_CA_B64"); v != "" {
		return v
	}
	for _, p := range []string{"/opt/flp/ca.crt", "/opt/flp/gen/ca.crt"} {
		if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
			return base64.StdEncoding.EncodeToString(b)
		}
	}
	return ""
}

// --- small helpers ---

func run(ctx context.Context, name string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, name, args...).Output()
	return strings.TrimSpace(string(out)), err
}

func probeHTTP(ctx context.Context, url string) int {
	if url == "" {
		return 0
	}
	// -k: the proxy is self-signed; we only care that it answers on TLS.
	out, err := run(ctx, "curl", "-sk", "--max-time", "6", "-o", "/dev/null", "-w", "%{http_code}", url)
	if err != nil {
		return 0
	}
	var code int
	fmt.Sscanf(out, "%d", &code)
	return code
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
func nowRFC3339() string {
	// The container has a real clock; the CLI/UI just display it.
	return time.Now().UTC().Format(time.RFC3339)
}
func squish(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		s = s[:157] + "..."
	}
	return s
}

// MarshalJSON indents for the CLI's --output json convenience.
func (s Status) JSON() []byte { b, _ := json.MarshalIndent(s, "", "  "); return b }
