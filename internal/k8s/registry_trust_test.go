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
	cmd := registryCAInstallCmd(host, true, nil, 0)

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
	cmd := registryCAInstallCmd("10.241.0.4", false, targets, 0)

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

// A label containing a space truncated at the parser on the first live run:
// "F5 License Proxy" came back as "F5". Cosmetic there, but the same bug on a
// REQUIRED target would be severe — the Required lookup would miss and an
// unreachable registry would be silently downgraded to optional, which is the exact
// failure this gate exists to prevent.
func TestProbeLabelsSurviveTheWire(t *testing.T) {
	targets := []ProbeTarget{
		{Label: "F5 License Proxy", Host: "10.243.1.4", Port: "8443"},
		{Label: "private registry", Host: "10.243.0.4", Port: "443", Required: true},
	}
	script := nodeProbeScript(targets, 0)
	if strings.Contains(script, `probe "F5 License Proxy"`) {
		t.Error("a label with spaces must be encoded before it reaches the space-separated probe line")
	}

	// Round-trip: what the node emits must parse back to the same label the summary
	// looks up, or Required is silently lost.
	line := "ROKSBNKCTL_PROBE node=n1 label=" + labelForWire("private registry") +
		" host=10.243.0.4 port=443 dns=skipped-ip tcp=FAILED detail=no route"
	r, ok := parseProbeLine(line)
	if !ok {
		t.Fatal("probe line must parse")
	}
	if _, err := SummariseProbeResults([]NodeProbeResult{r}, targets); err == nil {
		t.Error("a REQUIRED multi-word target that is unreachable must still fail the install")
	}
}

// The retry loop is what issue #57 is about: a probe run ~73s after a Transit Gateway
// attach saw both targets unreachable, `bnk up` refused, and the path was healthy
// minutes later. A single TCP failure cannot tell "unreachable" from "not yet".
func TestNodeProbeScript_RetryBudget(t *testing.T) {
	targets := []ProbeTarget{{Label: "registry", Host: "10.243.0.4", Port: "443", Required: true}}

	s := nodeProbeScript(targets, 240)
	if !strings.Contains(s, "budget=240") {
		t.Errorf("the configured budget must reach the script; got:\n%s", s)
	}
	for _, want := range []string{"deadline=", "while :;", "sleep 10", `[ "$tcp" = ok ] && break`} {
		if !strings.Contains(s, want) {
			t.Errorf("retry loop missing %q; got:\n%s", want, s)
		}
	}
	// A success must end the loop immediately, so a healthy path costs one attempt and
	// the budget is only ever paid by a failing target.
	if !strings.Contains(s, `attempts=$((attempts+1))`) {
		t.Errorf("attempts must be counted, so a failure can say how long it kept failing; got:\n%s", s)
	}

	// 0 is one-shot, and must still be a well-formed script rather than a special case.
	z := nodeProbeScript(targets, 0)
	if !strings.Contains(z, "budget=0") {
		t.Errorf("a 0 budget must be honoured verbatim; got:\n%s", z)
	}
	// A negative value is nonsense the config layer already rejects; clamp rather than
	// emitting `budget=-5`, which would make `date +%s >= deadline` true immediately in
	// a way that only looks intentional.
	if n := nodeProbeScript(targets, -5); !strings.Contains(n, "budget=0") {
		t.Errorf("a negative budget must clamp to 0; got:\n%s", n)
	}
}

// The verdict must belong to THIS run.
//
// The probe runs once per pod and then sleeps forever, and CollectNodeProbeResults
// reads that pod's log. When the pod template did not change, the DaemonSet did not
// roll, the pods kept sleeping, and a re-run after fixing the routing was shown the
// ORIGINAL failure — with nothing to indicate it was a recording (issue #57).
func TestPodTemplateAnnotations_RunIDForcesARoll(t *testing.T) {
	a := podTemplateAnnotations("cafe1234", "run-a")
	b := podTemplateAnnotations("cafe1234", "run-b")
	if a[registryTrustHashAnnot] != b[registryTrustHashAnnot] {
		t.Fatal("precondition: the CA hash is meant to be identical between these two runs")
	}
	if a[registryTrustRunAnnot] == b[registryTrustRunAnnot] {
		t.Error("identical inputs must still roll the DaemonSet, or the probe result is a recording")
	}

	// No run id keeps the historical behaviour — roll only when the inputs change.
	// Callers that only want the CA installed have no verdict to keep fresh.
	if _, ok := podTemplateAnnotations("cafe1234", "")[registryTrustRunAnnot]; ok {
		t.Error("an empty run id must not add the annotation")
	}
}

// The retry budget is part of the pod's behaviour, so changing it must change the pod
// template — otherwise raising the budget in config.yaml would leave the old script
// running until something else happened to roll the DaemonSet.
func TestRegistryCAInstallCmd_BudgetIsPartOfTheCommand(t *testing.T) {
	targets := []ProbeTarget{{Label: "registry", Host: "10.241.0.4", Port: "443", Required: true}}
	if a, b := registryCAInstallCmd("10.241.0.4", false, targets, 180),
		registryCAInstallCmd("10.241.0.4", false, targets, 600); a == b {
		t.Error("a changed retry budget must change the container command")
	}
}
