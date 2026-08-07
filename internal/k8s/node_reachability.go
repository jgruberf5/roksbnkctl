package k8s

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Per-node reachability probing, carried by the SAME privileged DaemonSet that
// installs the registry CA.
//
// WHY FROM A NODE. The operator host is the wrong vantage point and will lie. It sits
// on the services VPC with Internet egress; the workers are air-gapped behind a
// transit gateway. A probe that runs where roksbnkctl runs returns a confident green
// for a mirror the cluster cannot route to at all. The question is only ever "can THIS
// NODE reach it", and a DaemonSet is one pod per node — which on ROKS means every
// availability zone, without having to enumerate them.
//
// WHY IT MATTERS. Unreachable dependencies are discovered late and described wrongly:
// an unroutable mirror surfaces as ImagePullBackOff and then `cert_manager: context
// deadline exceeded` ten minutes later, and an unreachable licence proxy surfaces as a
// License CR that never goes Active until a 15-minute wait expires. Neither message
// names the network. A probe that runs first turns both into one line naming the node,
// the endpoint, and whether it was DNS or TCP that failed.
//
// A TGW attachment or a security group can be right in two zones and wrong in the
// third; that is precisely the failure this catches, and precisely the one a
// single-vantage check misses.

// ProbeTarget is one endpoint to check from every node.
type ProbeTarget struct {
	Label string // human name for messages, e.g. "registry" or "F5 License Proxy"
	Host  string // hostname or IP
	Port  string // e.g. "443", "8443"

	// Required marks a target whose failure must fail the install. The mirror is
	// required — a disconnected cluster cannot install from a registry it cannot
	// reach, so continuing only defers the failure to a worse message.
	Required bool
}

// NodeProbeResult is one node's verdict for one target.
type NodeProbeResult struct {
	Node   string
	Label  string
	Host   string
	Port   string
	DNS    string // ok | FAILED | skipped-ip
	TCP    string // ok | FAILED | skipped
	Detail string
}

// OK reports whether this node reached this target.
func (r NodeProbeResult) OK() bool { return r.TCP == "ok" }

func (r NodeProbeResult) String() string {
	s := fmt.Sprintf("%s -> %s (%s:%s): dns=%s tcp=%s", r.Node, r.Label, r.Host, r.Port, r.DNS, r.TCP)
	if r.Detail != "" {
		s += " (" + r.Detail + ")"
	}
	return s
}

// nodeProbeScript builds the shell the DaemonSet runs on each node.
//
// It deliberately uses a TOOL CHAIN rather than assuming one binary: the image is
// whatever is already cached on the node (so the probe needs no egress), and that image
// is not ours to choose. A pure TCP connect comes first (bash's /dev/tcp, then nc)
// because it answers precisely the question being asked; curl is the last resort, since
// it answers a different one and its timeout is ambiguous. If none of the three exist
// the probe says so rather than inventing a verdict.
//
// DNS is checked separately from TCP because they fail for different reasons and want
// different fixes: DNS is a resolver/hostname problem, TCP is routing, security groups
// or a gateway attachment.
func nodeProbeScript(targets []ProbeTarget) string {
	var b strings.Builder
	b.WriteString(`probe() { lbl="$1"; h="$2"; p="$3"; dns=skipped-ip; tcp=FAILED; detail=""; `)
	// A bare IPv4 needs no resolution: "dns=ok" for an IP would be noise and
	// "dns=FAILED" would be wrong.
	b.WriteString(`case "$h" in *[!0-9.]*) `)
	b.WriteString(`if getent hosts "$h" >/dev/null 2>&1; then dns=ok; else dns=FAILED; detail="name does not resolve from this node"; fi ;; `)
	b.WriteString(`esac; `)
	b.WriteString(`if [ "$dns" != FAILED ]; then `)
	// PURE TCP FIRST, and curl only as a last resort.
	//
	// The question this preflight asks is "can this node open a connection", and a TCP
	// connect answers exactly that. curl answers a DIFFERENT question — "does an HTTPS
	// service respond" — and conflates the two: probing an open port that does not
	// speak TLS makes curl sit until --connect-timeout and exit 28, which is
	// indistinguishable from never having connected. Verified by running this script
	// against a live SSH port, which curl reported as unreachable.
	//
	// TLS problems are real but they are a separate class with a separate fix, and
	// folding them into "unreachable" would send someone to the network for a
	// certificate problem.
	// Pick the tool ONCE, then trust its verdict.
	//
	// An earlier cut chained these with `&&`, so a tool that RAN and said "cannot
	// connect" fell through to the next one — and a closed port ended up reported by
	// curl as "may be open but not speaking TLS; inconclusive", which is both wrong and
	// unactionable. "The tool is absent" and "the tool answered no" are different
	// facts; only the first justifies trying something else.
	b.WriteString(`  if command -v bash >/dev/null 2>&1; then `)
	b.WriteString(`    if timeout 6 bash -c "exec 3<>/dev/tcp/$h/$p" >/dev/null 2>&1; then tcp=ok; `)
	b.WriteString(`    else detail="tcp connect failed -- refused, filtered, or no route"; fi; `)
	b.WriteString(`  elif command -v nc >/dev/null 2>&1; then `)
	b.WriteString(`    if nc -z -w 6 "$h" "$p" >/dev/null 2>&1; then tcp=ok; `)
	b.WriteString(`    else detail="tcp connect failed -- refused, filtered, or no route"; fi; `)
	b.WriteString(`  elif command -v curl >/dev/null 2>&1; then `)
	b.WriteString(`    curl -sk --connect-timeout 6 --max-time 10 -o /dev/null "https://$h:$p/" >/dev/null 2>&1; rc=$?; `)
	// 0/22/35/60 all prove the transport came up (an HTTP status, or a TLS complaint).
	b.WriteString(`    case "$rc" in 0|22|35|60) tcp=ok ;; `)
	b.WriteString(`      6) dns=FAILED; detail="curl could not resolve" ;; `)
	b.WriteString(`      7) detail="connection refused or no route" ;; `)
	b.WriteString(`      28) detail="curl timed out -- no TCP connection, or the port does not speak TLS" ;; `)
	b.WriteString(`      *) detail="curl exit $rc" ;; esac; `)
	b.WriteString(`  else tcp=skipped; detail="no bash, nc or curl on this node image"; fi; `)
	b.WriteString(`fi; `)
	b.WriteString(`echo "ROKSBNKCTL_PROBE node=${NODE_NAME} label=${lbl} host=${h} port=${p} dns=${dns} tcp=${tcp} detail=${detail}"; }; `)
	for _, t := range targets {
		// The probe line is space-separated key=value, so a label containing a space
		// truncates at the parser: "F5 License Proxy" came back as "F5" on the first
		// live run, and the two stray words were silently dropped as unknown fields.
		// Collapse whitespace at the boundary rather than trusting callers.
		fmt.Fprintf(&b, `probe %q %q %q; `, labelForWire(t.Label), t.Host, t.Port)
	}
	return b.String()
}

// labelForWire makes a label safe for the space-separated probe line.
func labelForWire(s string) string {
	return strings.Join(strings.Fields(s), "-")
}

// parseProbeLine reads one ROKSBNKCTL_PROBE line back into a result.
func parseProbeLine(line string) (NodeProbeResult, bool) {
	const marker = "ROKSBNKCTL_PROBE "
	i := strings.Index(line, marker)
	if i < 0 {
		return NodeProbeResult{}, false
	}
	r := NodeProbeResult{}
	rest := line[i+len(marker):]
	// detail is last and may contain spaces, so peel it off first.
	if j := strings.Index(rest, "detail="); j >= 0 {
		r.Detail = strings.TrimSpace(rest[j+len("detail="):])
		rest = rest[:j]
	}
	for _, kv := range strings.Fields(rest) {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		switch k {
		case "node":
			r.Node = v
		case "label":
			r.Label = v
		case "host":
			r.Host = v
		case "port":
			r.Port = v
		case "dns":
			r.DNS = v
		case "tcp":
			r.TCP = v
		}
	}
	return r, r.Label != ""
}

// CollectNodeProbeResults reads the probe output back from every DaemonSet pod.
//
// Results come from pod LOGS rather than an exit code on purpose: the probe must not
// fail the pod. A non-Ready DaemonSet would stall the CA install that shares it, and
// would collapse "this node cannot reach the mirror" into "the DaemonSet is unhealthy"
// — losing exactly the detail that makes the failure actionable.
func (c *Client) CollectNodeProbeResults(ctx context.Context) ([]NodeProbeResult, error) {
	// WAIT FOR FULL COVERAGE FIRST.
	//
	// Reading whatever pods exist at this instant is not enough. A pod still starting
	// — or mid-rollout after the target set changed — contributes nothing, and its
	// node then vanishes from the summary entirely. Observed live: three nodes, three
	// Ready pods, all three with correct results in their logs, and the collection
	// still returned only two because the third was replaced moments earlier.
	//
	// Harmless when the reported nodes all fail. Dangerous the other way round: two
	// nodes pass, the third (which would have failed) is missing, the summary reads
	// "2/2 reachable" and the gate passes blind — losing precisely the per-AZ break
	// this exists to catch. So require a result from every scheduled pod, and say so
	// plainly if that never happens.
	want, err := c.expectedProbeNodes(ctx)
	if err != nil {
		return nil, err
	}
	var results []NodeProbeResult
	deadline := time.Now().Add(2 * time.Minute)
	for {
		results, err = c.collectProbeResultsOnce(ctx)
		if err != nil {
			return nil, err
		}
		nodes := map[string]bool{}
		for _, r := range results {
			if r.Node != "" {
				nodes[r.Node] = true
			}
		}
		if want == 0 || len(nodes) >= want {
			return results, nil
		}
		if time.Now().After(deadline) {
			return results, fmt.Errorf(
				"only %d of %d nodes reported a reachability result -- the check cannot vouch for the rest, "+
					"and a node that never reports is indistinguishable from one that would have failed. "+
					"Check the %s DaemonSet in namespace %s",
				len(nodes), want, registryTrustName, registryTrustNamespace)
		}
		select {
		case <-ctx.Done():
			return results, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

// expectedProbeNodes is how many pods the DaemonSet intends to have — i.e. how many
// nodes must report before the verdict is complete.
func (c *Client) expectedProbeNodes(ctx context.Context) (int, error) {
	ds, err := c.clientset.AppsV1().DaemonSets(registryTrustNamespace).Get(ctx, registryTrustName, metav1.GetOptions{})
	if err != nil {
		return 0, fmt.Errorf("reading the %s DaemonSet to learn how many nodes must report: %w", registryTrustName, err)
	}
	return int(ds.Status.DesiredNumberScheduled), nil
}

func (c *Client) collectProbeResultsOnce(ctx context.Context) ([]NodeProbeResult, error) {
	pods, err := c.clientset.CoreV1().Pods(registryTrustNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=" + registryTrustName,
	})
	if err != nil {
		return nil, fmt.Errorf("listing probe pods: %w", err)
	}
	var out []NodeProbeResult
	for i := range pods.Items {
		p := &pods.Items[i]
		req := c.clientset.CoreV1().Pods(registryTrustNamespace).GetLogs(p.Name, &corev1.PodLogOptions{})
		rc, err := req.Stream(ctx)
		if err != nil {
			// One unreadable pod must not sink the whole check; record it as unknown.
			out = append(out, NodeProbeResult{
				Node: p.Spec.NodeName, Label: "(log unavailable)", DNS: "?", TCP: "?",
				Detail: err.Error(),
			})
			continue
		}
		sc := bufio.NewScanner(rc)
		for sc.Scan() {
			if r, ok := parseProbeLine(sc.Text()); ok {
				if r.Node == "" {
					r.Node = p.Spec.NodeName
				}
				out = append(out, r)
			}
		}
		_ = rc.Close()
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Label != out[j].Label {
			return out[i].Label < out[j].Label
		}
		return out[i].Node < out[j].Node
	})
	return out, nil
}

// SummariseProbeResults renders the per-node verdict, and reports whether any REQUIRED
// target was unreachable from any node.
//
// Every node is listed, not just the failures: "3 of 3 nodes reached the mirror" is the
// line that stops someone chasing the network when the fault is elsewhere, and a
// per-zone split is only visible if the passes are shown too.
func SummariseProbeResults(results []NodeProbeResult, targets []ProbeTarget) (string, error) {
	// Keyed on the WIRE form: results come back carrying labelForWire(Label), so
	// looking them up by the human label would never match and every target would
	// silently be treated as optional — including the registry.
	required := map[string]bool{}
	for _, t := range targets {
		required[labelForWire(t.Label)] = t.Required
	}
	byLabel := map[string][]NodeProbeResult{}
	for _, r := range results {
		byLabel[r.Label] = append(byLabel[r.Label], r)
	}

	var b strings.Builder
	var failedRequired []string
	labels := make([]string, 0, len(byLabel))
	for l := range byLabel {
		labels = append(labels, l)
	}
	sort.Strings(labels)

	for _, l := range labels {
		rs := byLabel[l]
		okCount := 0
		for _, r := range rs {
			if r.OK() {
				okCount++
			}
		}
		fmt.Fprintf(&b, "  %s: %d/%d nodes reachable\n", l, okCount, len(rs))
		for _, r := range rs {
			mark := "✓"
			if !r.OK() {
				mark = "✗"
			}
			fmt.Fprintf(&b, "    %s %s\n", mark, r.String())
		}
		if okCount < len(rs) && required[l] {
			failedRequired = append(failedRequired,
				fmt.Sprintf("%s unreachable from %d of %d nodes", l, len(rs)-okCount, len(rs)))
		}
	}

	if len(failedRequired) == 0 {
		return b.String(), nil
	}
	return b.String(), fmt.Errorf(
		"node reachability check failed: %s\n\n"+
			"  Installing would fail later and more obscurely — an unreachable mirror surfaces as\n"+
			"  ImagePullBackOff and then a helm timeout (`context deadline exceeded`), naming neither\n"+
			"  the registry nor the node. Fix the path first.\n\n"+
			"  dns=FAILED  the node cannot resolve the name. Check cluster DNS, or address the\n"+
			"              endpoint by IP (which is what `roksbnkctl flp output` emits).\n"+
			"  tcp=FAILED  the name resolves but the node cannot connect. Check the transit gateway\n"+
			"              attachment, the security groups, and that the VPCs' address prefixes do\n"+
			"              not overlap (an overlap is silently blackholed -- see issue #46).\n\n"+
			"  A per-zone split (some nodes pass, others fail) almost always means a subnet or\n"+
			"  security group that was only fixed in some availability zones",
		strings.Join(failedRequired, "; "))
}

// SplitHostPort returns host and port for a probe target, defaulting the port when the
// endpoint carries none. Accepts "10.241.0.4", "10.241.0.4:8443",
// "https://10.241.0.4:8443" and bare hostnames alike.
func SplitHostPort(endpoint, defaultPort string) (string, string) {
	e := strings.TrimSpace(endpoint)
	e = strings.TrimPrefix(strings.TrimPrefix(e, "https://"), "http://")
	if i := strings.IndexAny(e, "/"); i >= 0 {
		e = e[:i]
	}
	if h, p, err := net.SplitHostPort(e); err == nil && p != "" {
		return h, p
	}
	return e, defaultPort
}
