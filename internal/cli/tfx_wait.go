package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/util/jsonpath"
)

// `tfx wait` replaces the modules' GET-poll loops (cnecontroller_ready,
// license_active, FLO readiness) — the deterministic curl+grep+tr polls that
// replaced the brittle provider `wait_for`. Polling, backoff, and the bound live
// in Go: observable (structured log lines) and identical on Windows/Linux.

var (
	flagWaitGVR      string
	flagWaitNS       string
	flagWaitName     string
	flagWaitFor      string
	flagWaitTimeout  time.Duration
	flagWaitInterval time.Duration
	flagWaitMode     string
)

var tfxWaitCmd = &cobra.Command{
	Use:   "wait",
	Short: "Poll a resource until a condition or jsonpath matches (internal)",
	Long: `Polls a single resource until it satisfies --for, or --timeout elapses.

  --for condition=<Type>=<Status>   e.g. condition=CNEControllerAvailable=True
  --for jsonpath=<path>=<value>     e.g. jsonpath=status.state=Active

A not-yet-created resource is not an error — wait keeps polling until it appears
and matches, or the timeout fires (exit non-zero).`,
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runTFXWaitCmd,
}

func init() {
	f := tfxWaitCmd.Flags()
	f.StringVar(&flagWaitGVR, "gvr", "", "group/version/resource, e.g. k8s.f5.com/v1/cneinstances (required)")
	f.StringVar(&flagWaitNS, "ns", "", "namespace (empty = cluster-scoped)")
	f.StringVar(&flagWaitName, "name", "", "resource name (required)")
	f.StringVar(&flagWaitFor, "for", "", "condition=<Type>=<Status> or jsonpath=<path>=<value> (required)")
	f.DurationVar(&flagWaitTimeout, "timeout", 15*time.Minute, "overall wait budget")
	f.DurationVar(&flagWaitInterval, "interval", 5*time.Second, "poll interval (mode=poll, or the watch fallback)")
	f.StringVar(&flagWaitMode, "mode", "watch", "watch (event-driven; falls back to poll) or poll (GET loop)")
	tfxCmd.AddCommand(tfxWaitCmd)
}

func runTFXWaitCmd(cmd *cobra.Command, _ []string) error {
	if flagWaitGVR == "" || flagWaitName == "" || flagWaitFor == "" {
		return fmt.Errorf("--gvr, --name and --for are all required")
	}
	gvr, err := parseGVR(flagWaitGVR)
	if err != nil {
		return err
	}
	m, err := parseWaitFor(flagWaitFor)
	if err != nil {
		return err
	}
	dc, err := tfxDynamic()
	if err != nil {
		return err
	}
	ri := tfxResource(dc, gvr, flagWaitNS)
	return runTFXWait(cmdContext(cmd), ri, flagWaitName, m, flagWaitTimeout, flagWaitInterval, flagWaitMode, os.Stderr)
}

// errTFXWaitTimeout signals the watch strategy's OWN deadline fired — a genuine
// wait timeout — as opposed to the watch failing to establish (which triggers the
// poll fallback). Kept internal to the dispatcher.
var errTFXWaitTimeout = errors.New("tfx wait: watch deadline exceeded")

// dnsFailFastAttempts is how many consecutive resolution failures to tolerate before
// declaring the host unreachable. A few, because real DNS does blip; not many,
// because a name that is wrong is wrong forever and the alternative is burning the
// whole --timeout in silence.
const dnsFailFastAttempts = 3

// isDNSUnresolvable reports whether err is a name-resolution failure as opposed to a
// connection or TLS problem. A refused connection or a timeout may well clear; a name
// that does not exist will not.
func isDNSUnresolvable(err error) bool {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return dnsErr.IsNotFound || strings.Contains(dnsErr.Err, "no such host")
	}
	return strings.Contains(err.Error(), "no such host")
}

// runTFXWait dispatches the wait strategy. Default "watch" is event-driven: the
// API server streams the state change (the "webhook-style notification" for the
// k8s-side waits — CNEControllerAvailable, License Active, namespace phase), so we
// react in <1s instead of GET-sampling every interval. If the watch can't be
// established (RBAC, a resource that doesn't support watch, a dropped connection),
// it falls back to the bounded GET-poll — so every wait has a working path.
// "poll" forces the GET loop (for non-watchable or degraded environments).
func runTFXWait(ctx context.Context, ri dynamic.ResourceInterface, name string, m waitMatcher, timeout, interval time.Duration, mode string, logw io.Writer) error {
	if mode == "poll" {
		return runTFXWaitPoll(ctx, ri, name, m, timeout, interval, logw)
	}
	err := runTFXWaitWatch(ctx, ri, name, m, timeout, logw)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errTFXWaitTimeout):
		return fmt.Errorf("timed out after %s waiting for %s to satisfy [%s]", timeout, name, m)
	case ctx.Err() != nil:
		return err // caller cancelled — don't retry
	default:
		fmt.Fprintf(logw, "tfx wait: watch unavailable (%v); falling back to poll\n", err)
		return runTFXWaitPoll(ctx, ri, name, m, timeout, interval, logw)
	}
}

// runTFXWaitWatch is the event-driven strategy: GET (to catch an already-satisfied
// object — no missed-event race, the same guarantee the poll's first GET gives, and
// to learn the resourceVersion) then WATCH from that version, evaluating the matcher
// on each event. A dropped/expired watch is re-established (relisting via a fresh GET
// on an expired resourceVersion), so it survives the insecure ROKS public endpoint.
// Returns errTFXWaitTimeout when its own deadline fires; any other non-nil error
// means the watch could not do its job and the caller falls back to poll.
func runTFXWaitWatch(ctx context.Context, ri dynamic.ResourceInterface, name string, m waitMatcher, timeout time.Duration, logw io.Writer) error {
	wctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	sel := fields.OneTermEqualSelector("metadata.name", name).String()
	rv := ""
	for {
		// (Re)sync from a GET when we have no resourceVersion: catches the current
		// state (already-satisfied, or an object created since we started) and
		// anchors the watch. NotFound is fine — watch from latest; it may appear.
		if rv == "" {
			obj, err := ri.Get(wctx, name, metav1.GetOptions{})
			switch {
			case err == nil:
				if matched, desc := m.matched(obj); matched {
					fmt.Fprintf(logw, "tfx wait: %s satisfied [%s] (%s)\n", name, m, desc)
					return nil
				}
				rv = obj.GetResourceVersion()
			case apierrors.IsNotFound(err):
				fmt.Fprintf(logw, "tfx wait: %s not found yet -- watching for it\n", name)
			case wctx.Err() != nil && ctx.Err() == nil:
				return errTFXWaitTimeout
			default:
				return err // can't even GET → caller falls back to poll
			}
		}

		w, err := ri.Watch(wctx, metav1.ListOptions{FieldSelector: sel, ResourceVersion: rv})
		if err != nil {
			return err // watch couldn't establish → caller falls back to poll
		}
		done, ferr := tfxConsumeWatch(wctx, w, name, m, &rv, logw)
		w.Stop()
		switch {
		case done:
			return nil
		case ferr != nil:
			return ferr // real watch error → caller falls back to poll
		case wctx.Err() != nil:
			return errTFXWaitTimeout
		}
		// Channel closed cleanly (watch expired) — loop and re-establish. rv may
		// have been reset to "" by an Expired/Gone error, forcing a fresh GET.
	}
}

// tfxConsumeWatch drains one watch until the matcher is satisfied (done=true),
// the watch errors, or the channel closes / the context fires (done=false). It
// advances *rv so a re-watch resumes where this one stopped; an Expired/Gone
// watch error resets *rv to "" so the caller relists via GET.
func tfxConsumeWatch(ctx context.Context, w watch.Interface, name string, m waitMatcher, rv *string, logw io.Writer) (bool, error) {
	ch := w.ResultChan()
	for {
		select {
		case <-ctx.Done():
			return false, nil
		case ev, ok := <-ch:
			if !ok {
				return false, nil // channel closed → re-establish
			}
			if ev.Type == watch.Error {
				err := apierrors.FromObject(ev.Object)
				if apierrors.IsResourceExpired(err) || apierrors.IsGone(err) {
					*rv = "" // relist from latest on the next iteration
					return false, nil
				}
				return false, err
			}
			u, ok := ev.Object.(*unstructured.Unstructured)
			if !ok || u.GetName() != name {
				continue
			}
			*rv = u.GetResourceVersion()
			if ev.Type == watch.Deleted {
				fmt.Fprintf(logw, "tfx wait: %s deleted while waiting\n", name)
				continue
			}
			matched, desc := m.matched(u)
			if matched {
				fmt.Fprintf(logw, "tfx wait: %s satisfied [%s] (%s)\n", name, m, desc)
				return true, nil
			}
			if why, terminal := terminalWaitDiagnosis(desc); terminal {
				return false, fmt.Errorf("%s cannot become ready: %s", name, why)
			}
			fmt.Fprintf(logw, "tfx wait: %s not ready -- %s (want [%s])\n", name, desc, m)
		}
	}
}

// runTFXWaitPoll is the fallback GET-poll loop: get → match → sleep, bounded by
// timeout. A NotFound is treated as "not ready yet" (the resource may not exist at
// the start of the wait), never a hard failure — only the timeout fails.
func runTFXWaitPoll(ctx context.Context, ri dynamic.ResourceInterface, name string, m waitMatcher, timeout, interval time.Duration, logw io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	attempt := 0
	unresolvable := 0
	for {
		attempt++
		obj, err := ri.Get(ctx, name, metav1.GetOptions{})
		// A host that does not RESOLVE is not a transient blip: retrying it for the
		// full timeout is how "licensing hangs for 15 minutes" happens, when the real
		// fault was a malformed or wrong --kube-host. Give DNS a few tries in case it
		// is a genuine hiccup, then stop and say exactly what could not be resolved.
		if err != nil && isDNSUnresolvable(err) {
			unresolvable++
			if unresolvable >= dnsFailFastAttempts {
				return fmt.Errorf(
					"the Kubernetes API host cannot be resolved after %d attempts: %w"+
						" -- this is not transient, so waiting out the remaining timeout would not help;"+
						" check the --kube-host this command was invoked with, since an EMPTY value from"+
						" the caller shifts the argument list and lands a flag name in the host position",
					unresolvable, err)
			}
		} else if err == nil {
			unresolvable = 0
		}
		switch {
		case err == nil:
			if ok, desc := m.matched(obj); ok {
				fmt.Fprintf(logw, "tfx wait: %s satisfied [%s] (%s)\n", name, m, desc)
				return nil
			} else {
				if why, terminal := terminalWaitDiagnosis(desc); terminal {
					return fmt.Errorf("%s cannot become ready: %s", name, why)
				}
				fmt.Fprintf(logw, "tfx wait: %s not ready -- %s (want [%s], attempt %d)\n", name, desc, m, attempt)
			}
		case apierrors.IsNotFound(err):
			fmt.Fprintf(logw, "tfx wait: %s not found yet (attempt %d)\n", name, attempt)
		case ctx.Err() != nil:
			// fall through to the deadline check below
		default:
			fmt.Fprintf(logw, "tfx wait: get %s failed (attempt %d): %v\n", name, attempt, err)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out after %s waiting for %s to satisfy [%s]", timeout, name, m)
		case <-time.After(interval):
		}
	}
}

// waitMatcher reports whether a fetched object satisfies the wait, plus a short
// current-state string for the log line.
type waitMatcher interface {
	matched(obj *unstructured.Unstructured) (bool, string)
	fmt.Stringer
}

// parseWaitFor parses the --for value into a matcher.
func parseWaitFor(s string) (waitMatcher, error) {
	kind, rest, ok := strings.Cut(s, "=")
	if !ok {
		return nil, fmt.Errorf("invalid --for %q: want condition=<Type>=<Status> or jsonpath=<path>=<value>", s)
	}
	switch kind {
	case "condition":
		typ, status, ok := strings.Cut(rest, "=")
		if !ok || typ == "" {
			return nil, fmt.Errorf("invalid --for %q: condition needs <Type>=<Status>", s)
		}
		return conditionMatcher{typ: typ, status: status}, nil
	case "jsonpath":
		expr, want, ok := strings.Cut(rest, "=")
		if !ok || expr == "" {
			return nil, fmt.Errorf("invalid --for %q: jsonpath needs <path>=<value>", s)
		}
		tmpl := expr
		if !strings.HasPrefix(tmpl, "{") {
			tmpl = "{." + expr + "}"
		}
		jp := jsonpath.New("tfx").AllowMissingKeys(true)
		if err := jp.Parse(tmpl); err != nil {
			return nil, fmt.Errorf("invalid --for jsonpath %q: %w", expr, err)
		}
		return jsonpathMatcher{expr: expr, want: want, jp: jp}, nil
	default:
		return nil, fmt.Errorf("invalid --for %q: kind must be 'condition' or 'jsonpath'", s)
	}
}

// conditionMatcher matches a status.conditions[] entry of the given type whose
// status equals the wanted value — the standard k8s condition shape.
type conditionMatcher struct{ typ, status string }

func (c conditionMatcher) matched(obj *unstructured.Unstructured) (bool, string) {
	conds, found, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if !found {
		return false, "no status.conditions yet"
	}
	for _, raw := range conds {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if fmt.Sprint(m["type"]) == c.typ {
			st := fmt.Sprint(m["status"])
			// Carry the condition's reason and message, not just the status.
			// terminalWaitDiagnosis reads this string, and the scheduler's actual
			// verdict — "0/6 nodes are available: …" — lives in the message. With
			// only "Available=False" to look at, a permanently unschedulable pod
			// was indistinguishable from one still starting, so every such install
			// burned the full 15-minute timeout and then failed with a terraform
			// local-exec error naming nothing useful (#189).
			desc := fmt.Sprintf("%s=%s", c.typ, st)
			if r := strings.TrimSpace(fmt.Sprint(m["reason"])); r != "" && r != "<nil>" {
				desc += " reason=" + r
			}
			if msg := strings.TrimSpace(fmt.Sprint(m["message"])); msg != "" && msg != "<nil>" {
				desc += " msg=" + msg
			}
			return st == c.status, desc
		}
	}
	return false, fmt.Sprintf("%s not present", c.typ)
}

func (c conditionMatcher) String() string { return fmt.Sprintf("condition %s=%s", c.typ, c.status) }

// jsonpathMatcher matches when a jsonpath expression's value equals want.
type jsonpathMatcher struct {
	expr string
	want string
	jp   *jsonpath.JSONPath
}

func (j jsonpathMatcher) matched(obj *unstructured.Unstructured) (bool, string) {
	got, err := evalJSONPath(j.jp, obj.Object)
	if err != nil {
		return false, "jsonpath: " + err.Error()
	}
	return got == j.want, fmt.Sprintf("%s=%q", j.expr, got)
}

func (j jsonpathMatcher) String() string {
	return fmt.Sprintf("jsonpath %s=%s", j.expr, j.want)
}

// evalJSONPath evaluates a parsed jsonpath template against the object and joins
// the results (comma-separated when the path yields several, as kubectl does).
func evalJSONPath(jp *jsonpath.JSONPath, data interface{}) (string, error) {
	results, err := jp.FindResults(data)
	if err != nil {
		return "", err
	}
	var vals []string
	for _, group := range results {
		for _, v := range group {
			vals = append(vals, fmt.Sprint(v.Interface()))
		}
	}
	return strings.Join(vals, ","), nil
}

// unschedulableRe recognises the scheduler's "Insufficient <resource>" verdict
// inside a status-condition message.
//
// The operator republishes the failing pod's scheduling message verbatim, e.g.
//
//	pod f5-bnk/f5-tmm-…: 0/3 nodes are available: 3 Insufficient hugepages-2Mi
//
// so the diagnosis is already in the data `tfx wait` polls; nothing extra has to
// be queried to produce it.
var unschedulableRe = regexp.MustCompile(`(\d+)/(\d+) nodes are available: .*?Insufficient ([a-zA-Z0-9\-\./]+)`)

// pvNodeAffinityRe recognises the scheduler rejecting every node because the
// pod's PersistentVolume is bound elsewhere. Requires 0 available nodes: while
// some node still fits, the scheduler may yet place it.
var pvNodeAffinityRe = regexp.MustCompile(`0/(\d+) nodes are available:.*didn't match PersistentVolume's node affinity`)

func pvAffinityUnschedulable(desc string) bool {
	return pvNodeAffinityRe.MatchString(desc)
}

// terminalWaitDiagnosis reports whether a condition message describes a state
// that WAITING CANNOT FIX, and returns an explanation if so.
//
// A pod the scheduler has rejected for insufficient node resources will not
// become schedulable by waiting: no node gains capacity on its own. Before this,
// `bnk up` sat for the full 15-minute timeout and then failed with
//
//	f5-bnk-f5-cne-controller not ready -- Available=False
//
// which names neither the resource nor the pod. Twice during BNK 2.4 validation
// that cost fifteen minutes apiece to learn "the nodes have no hugepages".
//
// Deliberately narrow: only the scheduler's own "Insufficient <resource>"
// verdict. A pod that is Pending for an unbound PVC, an image pull, or a
// not-yet-created node genuinely may resolve by waiting, and failing those early
// would trade a slow success for a fast wrong answer.
func terminalWaitDiagnosis(desc string) (string, bool) {
	// A shared volume the replicas cannot all reach. Distinct from "Insufficient
	// <resource>" because no node ever gains the capacity to fix it: the pods are
	// pinned to separate nodes by anti-affinity while their PersistentVolume is
	// bound to one zone, so the scheduler is choosing between two constraints
	// that cannot both hold (#189).
	if pvAffinityUnschedulable(desc) {
		return "pods cannot be placed: their PersistentVolume is bound to one zone while " +
			"anti-affinity requires them on separate nodes, so no placement satisfies both.\n\n" +
			"  On 2.4 the TMM replicas share one ReadWriteOnce volume (tmm-pvc) while F5's own\n" +
			"  reference placement pins them to different nodes across different zones. RWO\n" +
			"  permits many pods on ONE node, which anti-affinity forbids — so at most one\n" +
			"  replica can ever bind it.\n\n" +
			"  Nothing in config.yaml resolves this today; see issue #189. What can be tried:\n\n" +
			"    bnk.storage_class_name: <a ReadWriteMany class>   # e.g. from vpc-file-csi-driver\n" +
			"    bnk.tmm_replicas: 1                               # one replica, one volume\n\n" +
			"  Note the controller currently applies storage_class_name to every CNEInstance\n" +
			"  volume EXCEPT tmm-pvc, so the first may not take effect.", true
	}

	m := unschedulableRe.FindStringSubmatch(desc)
	if m == nil {
		return "", false
	}
	avail, total, resource := m[1], m[2], m[3]
	if avail != "0" {
		// Some nodes still fit; the scheduler may yet place it.
		return "", false
	}

	msg := fmt.Sprintf("no node can run this pod: %s of %s nodes lack %q, and waiting will not create capacity.",
		avail, total, resource)
	if strings.HasPrefix(resource, "hugepages-") {
		msg += "\n\n" +
			"  BNK's deploymentSize decides how much it asks for. Tiny requests NO hugepages;\n" +
			"  Small requests 4Gi of hugepages-2Mi. A stock ROKS worker reports 0 — including\n" +
			"  F5's own reference cluster, which runs Tiny for exactly this reason.\n\n" +
			"  Two ways forward:\n\n" +
			"    bnk.cneinstance_size: Tiny      # ask for what the nodes already have\n\n" +
			"    bnk.hugepages:                  # or give the nodes what BNK asked for\n" +
			"      enabled: true\n" +
			"      count: 2048                   # x 2M = 4Gi per node\n\n" +
			"  bnk.hugepages allocates them through the Node Tuning Operator. Note that it\n" +
			"  sets a bootloader kernel argument, so the Machine Config Operator DRAINS AND\n" +
			"  REBOOTS every matching worker in turn — a maintenance event, not a config\n" +
			"  change. That is why it is off by default and you are reading this instead."
	}
	return msg, true
}
