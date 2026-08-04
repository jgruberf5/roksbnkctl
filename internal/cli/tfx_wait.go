package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
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
	return runTFXWait(cmd.Context(), ri, flagWaitName, m, flagWaitTimeout, flagWaitInterval, flagWaitMode, os.Stderr)
}

// errTFXWaitTimeout signals the watch strategy's OWN deadline fired — a genuine
// wait timeout — as opposed to the watch failing to establish (which triggers the
// poll fallback). Kept internal to the dispatcher.
var errTFXWaitTimeout = errors.New("tfx wait: watch deadline exceeded")

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
	for {
		attempt++
		obj, err := ri.Get(ctx, name, metav1.GetOptions{})
		switch {
		case err == nil:
			if ok, desc := m.matched(obj); ok {
				fmt.Fprintf(logw, "tfx wait: %s satisfied [%s] (%s)\n", name, m, desc)
				return nil
			} else {
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
			return st == c.status, fmt.Sprintf("%s=%s", c.typ, st)
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
