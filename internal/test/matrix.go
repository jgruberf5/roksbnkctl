package test

import (
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Matrix families.
const (
	FamilyIperf3 = "iperf3"
	FamilyL7     = "l7"
)

// Endpoint kinds.
const (
	EndpointVSI     = "vsi"     // an SSH jumphost — a traffic-source "VSI"
	EndpointAddress = "address" // host:port — an iperf3 TCP server (e.g. a TCPRoute VIP)
	EndpointURL     = "url"     // a full http(s):// URL — an HTTPRoute target
)

// MatrixSpec is the parsed matrix.yaml: the declarative perf grid the
// `roksbnkctl test matrix` runner executes against an already-deployed
// cluster. It owns no Terraform — the gateway block merely *names* the
// existing BNK gateway stack so the optional route fixtures can attach
// to it.
type MatrixSpec struct {
	Gateway   GatewayIdentity         `yaml:"gateway,omitempty"`
	Fixtures  FixturesSpec            `yaml:"fixtures,omitempty"`
	Endpoints map[string]EndpointSpec `yaml:"endpoints"`
	Cells     []CellSpec              `yaml:"cells"`
}

// GatewayIdentity names the already-deployed BNK gateway stack. Used
// only when fixtures.routes is true, to render TCPRoute / HTTPRoute
// fixtures that attach to the operator's existing Gateway. Never
// created or mutated by the runner.
type GatewayIdentity struct {
	ClassName      string `yaml:"class_name,omitempty"`
	ControllerName string `yaml:"controller_name,omitempty"`
	BnkGatewayName string `yaml:"bnkgateway_name,omitempty"`
	FloNamespace   string `yaml:"flo_namespace,omitempty"`
	AppNamespace   string `yaml:"app_namespace,omitempty"`

	// Name is the existing Gateway object the route fixtures attach to
	// (parentRefs.name). HTTPSection / HTTPSSection / TCPSection are the
	// listener sectionNames on that Gateway the http / https / tcp routes
	// bind to — they must already exist (the runner adds Routes, never
	// listeners). A section left empty means "don't render that route".
	Name         string `yaml:"name,omitempty"`
	HTTPSection  string `yaml:"http_section,omitempty"`
	HTTPSSection string `yaml:"https_section,omitempty"`
	TCPSection   string `yaml:"tcp_section,omitempty"`
}

// FixturesSpec toggles the ephemeral, runner-owned k8s objects. Each is
// deployed before the campaign and torn down after (unless --keep).
// These are the *only* cluster writes the matrix performs, and every
// one is skipped under --dry-run.
type FixturesSpec struct {
	Iperf3Server bool `yaml:"iperf3_server,omitempty"` // L4 server pod (reuses test throughput's fixture)
	HTTPBackend  bool `yaml:"http_backend,omitempty"`  // small HTTP file server for the L7 family
	Routes       bool `yaml:"routes,omitempty"`        // TCPRoute + HTTPRoute(+TLS) attaching to gateway
}

// EndpointSpec is one named (placement, role). The locality axis of the
// perf plan (same-zone / diff-zone / diff-VPC) is implicit in *which*
// jumphost a vsi endpoint targets — no separate locality enum.
type EndpointSpec struct {
	Kind   string `yaml:"kind"`             // vsi | address | url
	Target string `yaml:"target,omitempty"` // vsi: SSH target name (ssh:<target>)
	Host   string `yaml:"host,omitempty"`   // address: host or IP
	Port   int    `yaml:"port,omitempty"`   // address: TCP port (iperf3; default 5201)
	URL    string `yaml:"url,omitempty"`    // url: full http(s):// URL
}

// CellSpec is one row of the grid.
type CellSpec struct {
	Name   string `yaml:"name"`
	Family string `yaml:"family"`           // iperf3 | l7
	Client string `yaml:"client,omitempty"` // endpoint key (kind vsi); empty → run locally
	Server string `yaml:"server"`           // endpoint key (kind address for iperf3, url for l7)

	// iperf3 knobs.
	Length   string `yaml:"length,omitempty"`   // -l block size ("128", "512K")
	Bytes    string `yaml:"bytes,omitempty"`    // -n fixed transfer
	Duration int    `yaml:"duration,omitempty"` // -t seconds
	Streams  int    `yaml:"streams,omitempty"`  // -P parallel streams

	// l7 knobs.
	L7 L7CellSpec `yaml:"l7,omitempty"`
}

// L7CellSpec is the per-cell h2load knob block.
type L7CellSpec struct {
	Mode     string `yaml:"mode"`               // cps | tps | throughput
	Requests int    `yaml:"requests,omitempty"` // -n
	Duration int    `yaml:"duration,omitempty"` // -D seconds
	Clients  int    `yaml:"clients,omitempty"`  // -c
	Streams  int    `yaml:"streams,omitempty"`  // -m
	Threads  int    `yaml:"threads,omitempty"`  // -t
	HTTP1    bool   `yaml:"http1,omitempty"`    // --h1
}

// ParseMatrix unmarshals and validates a matrix.yaml.
func ParseMatrix(b []byte) (*MatrixSpec, error) {
	var spec MatrixSpec
	if err := yaml.Unmarshal(b, &spec); err != nil {
		return nil, fmt.Errorf("parsing matrix yaml: %w", err)
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	return &spec, nil
}

// Validate checks structural integrity: every cell references defined
// endpoints of the right kind for its family, knobs are sane, and the
// gateway identity is present when route fixtures are requested.
func (s *MatrixSpec) Validate() error {
	if len(s.Cells) == 0 {
		return fmt.Errorf("matrix: no cells defined")
	}
	if s.Fixtures.Routes {
		g := s.Gateway
		if g.AppNamespace == "" || g.Name == "" {
			return fmt.Errorf("matrix: fixtures.routes requires gateway.app_namespace and gateway.name")
		}
		if g.HTTPSection == "" && g.HTTPSSection == "" && g.TCPSection == "" {
			return fmt.Errorf("matrix: fixtures.routes requires at least one of gateway.http_section / https_section / tcp_section")
		}
	}
	for i, c := range s.Cells {
		where := fmt.Sprintf("cell[%d] %q", i, c.Name)
		if c.Name == "" {
			return fmt.Errorf("%s: name is required", where)
		}
		// Client endpoint, if named, must be a vsi.
		if c.Client != "" {
			ep, ok := s.Endpoints[c.Client]
			if !ok {
				return fmt.Errorf("%s: client endpoint %q not defined", where, c.Client)
			}
			if ep.Kind != EndpointVSI {
				return fmt.Errorf("%s: client endpoint %q must be kind %q (got %q)", where, c.Client, EndpointVSI, ep.Kind)
			}
			if ep.Target == "" {
				return fmt.Errorf("%s: client endpoint %q (vsi) needs a target", where, c.Client)
			}
		}
		srv, ok := s.Endpoints[c.Server]
		if !ok {
			return fmt.Errorf("%s: server endpoint %q not defined", where, c.Server)
		}
		switch c.Family {
		case FamilyIperf3:
			if srv.Kind != EndpointAddress {
				return fmt.Errorf("%s: iperf3 server endpoint %q must be kind %q (got %q)", where, c.Server, EndpointAddress, srv.Kind)
			}
			if srv.Host == "" {
				return fmt.Errorf("%s: iperf3 server endpoint %q needs a host", where, c.Server)
			}
		case FamilyL7:
			if srv.Kind != EndpointURL {
				return fmt.Errorf("%s: l7 server endpoint %q must be kind %q (got %q)", where, c.Server, EndpointURL, srv.Kind)
			}
			if srv.URL == "" {
				return fmt.Errorf("%s: l7 server endpoint %q needs a url", where, c.Server)
			}
			if c.L7.Mode == "" {
				return fmt.Errorf("%s: l7 cell needs l7.mode (cps|tps|throughput)", where)
			}
		default:
			return fmt.Errorf("%s: unknown family %q (want %s|%s)", where, c.Family, FamilyIperf3, FamilyL7)
		}
	}
	return nil
}

// ResolvedCell is a CellSpec with its endpoints resolved to a concrete
// backend + tool options, ready for the CLI layer to dispatch. It
// carries exactly one of Iperf3 / L7 depending on Family.
type ResolvedCell struct {
	Name        string
	Family      string
	BackendSpec string // "" (local) | ssh:<target>
	ClientLabel string // human label for the client placement
	ServerLabel string // human label for the server placement

	Iperf3 ThroughputOptions
	L7     L7Options
}

// Argv returns the tool argv (sans argv[0]) this cell will run. Pure —
// used by both the dry-run planner and the live dispatcher so the plan
// shows exactly what executes.
func (c ResolvedCell) Argv() ([]string, error) {
	switch c.Family {
	case FamilyIperf3:
		return Iperf3Args(c.Iperf3), nil
	case FamilyL7:
		return L7Args(c.L7)
	default:
		return nil, fmt.Errorf("cell %q: unknown family %q", c.Name, c.Family)
	}
}

// Tool returns the argv[0] binary for the cell's family.
func (c ResolvedCell) Tool() string {
	if c.Family == FamilyL7 {
		return "h2load"
	}
	return "iperf3"
}

// Expand resolves the spec into runnable cells, optionally filtered by a
// glob on cell name (matched with path.Match). An empty only matches
// everything.
func (s *MatrixSpec) Expand(only string) ([]ResolvedCell, error) {
	var out []ResolvedCell
	for _, c := range s.Cells {
		if only != "" {
			ok, err := path.Match(only, c.Name)
			if err != nil {
				return nil, fmt.Errorf("bad --only glob %q: %w", only, err)
			}
			if !ok {
				continue
			}
		}
		rc := ResolvedCell{Name: c.Name, Family: c.Family}

		// Client placement → backend.
		if c.Client == "" {
			rc.BackendSpec = ""
			rc.ClientLabel = "local"
		} else {
			ep := s.Endpoints[c.Client]
			rc.BackendSpec = "ssh:" + ep.Target
			rc.ClientLabel = "ssh:" + ep.Target
		}

		srv := s.Endpoints[c.Server]
		switch c.Family {
		case FamilyIperf3:
			port := srv.Port
			if port == 0 {
				port = 5201
			}
			rc.ServerLabel = fmt.Sprintf("%s:%d", srv.Host, port)
			rc.Iperf3 = ThroughputOptions{
				Mode:     "matrix",
				Endpoint: srv.Host,
				Port:     port,
				Length:   c.Length,
				Bytes:    c.Bytes,
				Duration: c.Duration,
				Streams:  c.Streams,
			}
		case FamilyL7:
			rc.ServerLabel = srv.URL
			rc.L7 = L7Options{
				URL:      srv.URL,
				Mode:     L7Mode(c.L7.Mode),
				Requests: c.L7.Requests,
				Duration: c.L7.Duration,
				Clients:  c.L7.Clients,
				Streams:  c.L7.Streams,
				Threads:  c.L7.Threads,
				HTTP1:    c.L7.HTTP1,
			}
		}
		out = append(out, rc)
	}
	if only != "" && len(out) == 0 {
		return nil, fmt.Errorf("--only %q matched no cells", only)
	}
	return out, nil
}

// PlanString renders the resolved grid as a human plan — the --dry-run
// output. No cluster calls; shows the exact (client backend, server,
// argv) per cell so an operator can confirm placement before a long run.
func PlanString(cells []ResolvedCell) string {
	var b strings.Builder
	fmt.Fprintf(&b, "matrix plan — %d cell(s)\n", len(cells))
	for i, c := range cells {
		argv, err := c.Argv()
		argvStr := strings.Join(argv, " ")
		if err != nil {
			argvStr = "ERROR: " + err.Error()
		}
		fmt.Fprintf(&b, "\n[%d] %s\n", i+1, c.Name)
		fmt.Fprintf(&b, "    family : %s\n", c.Family)
		fmt.Fprintf(&b, "    client : %s\n", c.ClientLabel)
		fmt.Fprintf(&b, "    server : %s\n", c.ServerLabel)
		fmt.Fprintf(&b, "    run    : %s %s\n", c.Tool(), argvStr)
	}
	return b.String()
}

// MatrixRun is the campaign report. Additive on roksbnkctl.v1 — one
// ProbeResult per cell, each carrying its family-specific Extra.
type MatrixRun struct {
	Schema     string        `json:"schema"`
	Command    string        `json:"command"`
	Suite      string        `json:"suite"`
	Timestamp  time.Time     `json:"timestamp"`
	DurationMS int64         `json:"duration_ms"`
	Cells      []ProbeResult `json:"cells"`
	Overall    Status        `json:"overall"`
}

// NewMatrixRun assembles a MatrixRun from per-cell probes.
func NewMatrixRun(start time.Time, cells []ProbeResult) MatrixRun {
	return MatrixRun{
		Schema:     SchemaVersion,
		Command:    "test",
		Suite:      "matrix",
		Timestamp:  start,
		DurationMS: time.Since(start).Milliseconds(),
		Cells:      cells,
		Overall:    Aggregate(cells),
	}
}

// WriteMatrixMarkdown renders the run as a Markdown grid shaped like the
// perf-plan table, so a campaign is visually diffable against the source
// document. iperf3 rows show Gbit/s + retransmits; l7 rows show req/s +
// Mbit/s + mean latency.
func WriteMatrixMarkdown(w io.Writer, run MatrixRun) {
	fmt.Fprintf(w, "## matrix (%s, %dms)\n\n", run.Overall, run.DurationMS)
	fmt.Fprintln(w, "| Cell | Family | Status | Throughput | req/s | mean ms | notes |")
	fmt.Fprintln(w, "|---|---|---|---|---|---|---|")
	for _, c := range run.Cells {
		thr, rps, ms := "—", "—", "—"
		notes := ""
		switch {
		case c.Extra["throughput_gbps"] != nil:
			thr = fmt.Sprintf("%.2f Gbit/s", asFloat(c.Extra["throughput_gbps"]))
			if rt := asInt(c.Extra["retransmits"]); rt > 0 {
				notes = fmt.Sprintf("%d retransmits", rt)
			}
		case c.Extra["throughput_mbps"] != nil:
			thr = fmt.Sprintf("%.1f Mbit/s", asFloat(c.Extra["throughput_mbps"]))
			rps = fmt.Sprintf("%.0f", asFloat(c.Extra["req_per_sec"]))
			ms = fmt.Sprintf("%.2f", asFloat(c.Extra["req_time_mean_ms"]))
			if f := asInt(c.Extra["failed"]); f > 0 {
				notes = fmt.Sprintf("%d failed", f)
			}
		}
		if notes == "" {
			notes = c.Detail
		}
		fmt.Fprintf(w, "| %s | %s | %s | %s | %s | %s | %s |\n",
			mdEsc(c.Name), c.Suite, c.Status, thr, rps, ms, mdEsc(notes))
	}
}

func asFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	default:
		return 0
	}
}

func asInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	default:
		return 0
	}
}

func mdEsc(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

// SortedEndpointKeys returns the endpoint names in deterministic order —
// handy for stable plan/diagnostic output.
func (s *MatrixSpec) SortedEndpointKeys() []string {
	keys := make([]string, 0, len(s.Endpoints))
	for k := range s.Endpoints {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
