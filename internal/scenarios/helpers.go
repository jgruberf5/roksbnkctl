package scenarios

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"text/template"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// HTTPRouteGVR is the GroupVersionResource for Gateway API HTTPRoute objects.
var HTTPRouteGVR = schema.GroupVersionResource{
	Group:    "gateway.networking.k8s.io",
	Version:  "v1",
	Resource: "httproutes",
}

// GatewayGVR is the GroupVersionResource for Gateway API Gateway objects.
var GatewayGVR = schema.GroupVersionResource{
	Group:    "gateway.networking.k8s.io",
	Version:  "v1",
	Resource: "gateways",
}

// RenderTemplate executes a Go text/template with the given data and returns
// the rendered string.
func RenderTemplate(tmpl string, data interface{}) (string, error) {
	t, err := template.New("manifest").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// WaitDeploymentAvailable polls until the named Deployment has the Available
// condition set to True, or timeout elapses.
func WaitDeploymentAvailable(ctx context.Context, sctx *Context, ns, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		dep, err := sctx.Clientset.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			for _, c := range dep.Status.Conditions {
				if string(c.Type) == "Available" && string(c.Status) == "True" {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return fmt.Errorf("deployment %s/%s not Available after %s", ns, name, timeout)
}

// WaitCondition polls a resource (identified by gvr) for a named condition
// with status=True in .status.conditions.
func WaitCondition(ctx context.Context, sctx *Context, gvr schema.GroupVersionResource, ns, name, condType string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		obj, err := sctx.Dynamic.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			conditions, _, _ := NestedSlice(obj.Object, "status", "conditions")
			for _, cRaw := range conditions {
				c, ok := cRaw.(map[string]interface{})
				if !ok {
					continue
				}
				if c["type"] == condType && c["status"] == "True" {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return fmt.Errorf("%s %s/%s condition %s not True after %s", gvr.Resource, ns, name, condType, timeout)
}

// WaitHTTPRouteCondition polls the HTTPRoute's .status.parents[*].conditions
// for the named condition with status=True.
func WaitHTTPRouteCondition(ctx context.Context, sctx *Context, ns, name, condType string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		obj, err := sctx.Dynamic.Resource(HTTPRouteGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			parents, _, _ := NestedSlice(obj.Object, "status", "parents")
			for _, pRaw := range parents {
				p, ok := pRaw.(map[string]interface{})
				if !ok {
					continue
				}
				conditions, _, _ := NestedSlice(p, "conditions")
				for _, cRaw := range conditions {
					c, ok2 := cRaw.(map[string]interface{})
					if !ok2 {
						continue
					}
					if c["type"] == condType && c["status"] == "True" {
						return nil
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return fmt.Errorf("HTTPRoute %s/%s condition %s not True after %s", ns, name, condType, timeout)
}

// BuildProbeParams extracts VIP, iteration count, and per-probe timeout from
// the scenario context options and cluster intent.
func BuildProbeParams(ctx *Context) (vip string, iterations int, timeout time.Duration, err error) {
	vip = ctx.Options["vip"]
	if vip == "" && ctx.Cluster != nil {
		vip, err = ctx.Cluster.DefaultVIP()
		if err != nil {
			return "", 0, 0, fmt.Errorf("deriving VIP: %w", err)
		}
	}
	if vip == "" {
		return "", 0, 0, fmt.Errorf("VIP not set — pass --vip or set network.dataPath.external.cidr")
	}
	iterations = 5
	if v := ctx.Options["iterations"]; v != "" {
		if n, e := strconv.Atoi(v); e == nil && n > 0 {
			iterations = n
		}
	}
	timeout = 10 * time.Second
	if v := ctx.Options["timeout"]; v != "" {
		if d, e := time.ParseDuration(v); e == nil && d > 0 {
			timeout = d
		}
	}
	return vip, iterations, timeout, nil
}

// FinalizeResult stamps Status and Summary onto res based on AllPassed().
func FinalizeResult(res Result) Result {
	if res.AllPassed() {
		res.Status = "ok"
		res.Summary = "control-plane reconciled + end-to-end curls via Gateway returned HTTP 200"
	} else {
		res.Status = "failed"
		var failed []string
		for _, a := range res.Assertions {
			if !a.OK {
				failed = append(failed, a.Description)
			}
		}
		res.Summary = "failed: " + strings.Join(failed, "; ")
	}
	return res
}

// ErrString returns a single-line string representation of err, or "" if nil.
func ErrString(err error) string {
	if err == nil {
		return ""
	}
	return oneLine(err.Error(), 200)
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// IsNotFound returns true when err is a not-found style error.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "not found")
}

// VIPPlus100 increments the last octet of a dotted-quad IP by 100 (capped at 254).
func VIPPlus100(vip string) string {
	parts := strings.Split(vip, ".")
	if len(parts) != 4 {
		return vip
	}
	last, err := strconv.Atoi(parts[3])
	if err != nil {
		return vip
	}
	end := last + 100
	if end > 254 {
		end = 254
	}
	parts[3] = strconv.Itoa(end)
	return strings.Join(parts, ".")
}
