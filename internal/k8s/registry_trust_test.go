package k8s

import "strings"

import "testing"

// TestRegistryCAInstallCmd guards the exact regression that broke air-gap
// pulls: the shell loop variable $d must survive into the container command
// (so the CA lands at <certs.d>/<host>/ca.crt on the node), and the host must
// be substituted by Go. A prior heredoc-generated DaemonSet ate $d, writing
// the CA to a bogus in-container path and leaving nodes with x509 "unknown
// authority".
func TestRegistryCAInstallCmd(t *testing.T) {
	host := "10.241.0.4"
	cmd := registryCAInstallCmd(host, true, nil)

	// $d must be present (as a live shell variable), not eaten.
	if !strings.Contains(cmd, `"$d/10.241.0.4"`) {
		t.Errorf("install cmd lost the $d loop variable; got:\n%s", cmd)
	}
	if !strings.Contains(cmd, `install -m644 /ca/ca.crt "$d/10.241.0.4/ca.crt"`) {
		t.Errorf("install cmd does not write ca.crt under $d/<host>; got:\n%s", cmd)
	}
	// It must target BOTH the CRI-O and docker trust dirs.
	for _, dir := range []string{"/host/etc/containers/certs.d", "/host/etc/docker/certs.d"} {
		if !strings.Contains(cmd, dir) {
			t.Errorf("install cmd missing trust dir %q; got:\n%s", dir, cmd)
		}
	}
	// A bare /<host> (the bug's signature — $d dropped) must NOT appear.
	if strings.Contains(cmd, ` /10.241.0.4/ca.crt`) || strings.Contains(cmd, `"/10.241.0.4`) {
		t.Errorf("install cmd writes to a $d-less absolute path (the bug); got:\n%s", cmd)
	}
}

// With no CA supplied the DaemonSet must still run, and still probe. A registry whose
// CA is already in the node bundle (or is publicly signed) needs no install — but
// reachability is the thing that fails silently, so skipping the whole pod would skip
// the only check that matters.
func TestRegistryCAInstallCmd_NoCAStillProbes(t *testing.T) {
	targets := []ProbeTarget{{Label: "registry", Host: "10.241.0.4", Port: "443", Required: true}}
	cmd := registryCAInstallCmd("10.241.0.4", false, targets)

	if strings.Contains(cmd, "install -m644 /ca/ca.crt") {
		t.Errorf("no CA was supplied, so nothing should be installed; got:\n%s", cmd)
	}
	if !strings.Contains(cmd, "ROKSBNKCTL_PROBE") {
		t.Errorf("the probe must still run when no CA is supplied; got:\n%s", cmd)
	}
	if !strings.Contains(cmd, "sleep infinity") {
		t.Errorf("the pod must stay Ready after probing; got:\n%s", cmd)
	}
	// `set -e` would abort the pod on a failed probe, making a reachability failure
	// look like an unhealthy DaemonSet instead of a named, per-node verdict.
	if strings.Contains(cmd, "set -e") {
		t.Errorf("install cmd must not `set -e` — a failed probe has to reach sleep infinity; got:\n%s", cmd)
	}
}

func TestParseProbeLine(t *testing.T) {
	line := "ROKSBNKCTL_PROBE node=kube-abc label=registry host=10.241.0.4 port=443 dns=skipped-ip tcp=FAILED detail=connect timed out (routing/security group?)"
	r, ok := parseProbeLine(line)
	if !ok {
		t.Fatal("a well-formed probe line must parse")
	}
	if r.Node != "kube-abc" || r.Label != "registry" || r.Host != "10.241.0.4" || r.Port != "443" {
		t.Errorf("fields mis-parsed: %+v", r)
	}
	if r.TCP != "FAILED" || r.OK() {
		t.Errorf("a FAILED tcp must not report OK: %+v", r)
	}
	// detail is last and contains spaces — it must survive whole.
	if r.Detail != "connect timed out (routing/security group?)" {
		t.Errorf("detail truncated at a space: %q", r.Detail)
	}
	if _, ok := parseProbeLine("some unrelated container log line"); ok {
		t.Error("non-probe output must not parse as a result")
	}
}

// A REQUIRED target unreachable from ANY node must fail the install; an optional one
// must not. And a per-zone split has to be visible, since that is the shape a
// single-vantage check misses.
func TestSummariseProbeResults(t *testing.T) {
	targets := []ProbeTarget{
		{Label: "registry", Required: true},
		{Label: "F5 License Proxy"},
	}
	partial := []NodeProbeResult{
		{Node: "n1", Label: "registry", TCP: "ok"},
		{Node: "n2", Label: "registry", TCP: "ok"},
		{Node: "n3", Label: "registry", TCP: "FAILED", Detail: "no route"},
	}
	summary, err := SummariseProbeResults(partial, targets)
	if err == nil {
		t.Error("a required target unreachable from one node must fail the install")
	}
	if !strings.Contains(summary, "2/3 nodes reachable") {
		t.Errorf("the per-node split must be visible; got:\n%s", summary)
	}

	// Optional target failing everywhere: reported, not fatal.
	optional := []NodeProbeResult{{Node: "n1", Label: "F5 License Proxy", TCP: "FAILED"}}
	if _, err := SummariseProbeResults(optional, targets); err != nil {
		t.Errorf("an optional target must not fail the install: %v", err)
	}

	// All good.
	allOK := []NodeProbeResult{{Node: "n1", Label: "registry", TCP: "ok"}}
	if _, err := SummariseProbeResults(allOK, targets); err != nil {
		t.Errorf("all reachable must pass: %v", err)
	}
}

func TestSplitHostPort(t *testing.T) {
	cases := []struct{ in, host, port string }{
		{"10.241.0.4", "10.241.0.4", "443"},
		{"10.241.0.4:5000", "10.241.0.4", "5000"},
		{"https://10.241.1.4:8443", "10.241.1.4", "8443"},
		{"https://flp.example.com", "flp.example.com", "443"},
		{"https://flp.example.com:8443/api", "flp.example.com", "8443"},
	}
	for _, c := range cases {
		h, p := SplitHostPort(c.in, "443")
		if h != c.host || p != c.port {
			t.Errorf("SplitHostPort(%q) = %q,%q want %q,%q", c.in, h, p, c.host, c.port)
		}
	}
}
