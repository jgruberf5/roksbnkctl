package flpstatus

import (
	"bufio"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
)

// ── podman backend (standalone VSI: the flp pod) ─────────────────────────────

type podmanBackend struct{}

func (p *podmanBackend) Kind() string     { return "vsi" }
func (p *podmanBackend) ProxyURL() string { return "https://localhost:8443/" }

func (p *podmanBackend) Container(ctx context.Context, name string) (Indicator, string) {
	// `podman ps -a` so exited containers still report. --format json is stable.
	out, err := run(ctx, "podman", "ps", "-a", "--filter", "name="+name, "--format", "json")
	if err != nil || out == "" || out == "[]" {
		return Unknown, "not found"
	}
	var arr []struct {
		Names  []string `json:"Names"`
		State  string   `json:"State"`
		Status string   `json:"Status"`
	}
	if json.Unmarshal([]byte(out), &arr) != nil || len(arr) == 0 {
		return Unknown, "unparseable"
	}
	c := arr[0]
	switch strings.ToLower(c.State) {
	case "running":
		return Up, c.Status
	case "created", "restarting", "paused":
		return Pending, c.Status
	default:
		return Down, c.Status
	}
}

func (p *podmanBackend) ProxyLog(ctx context.Context, n int) (string, error) {
	return run(ctx, "podman", "logs", "--tail", itoa(n), "f5-license-proxy")
}

func (p *podmanBackend) StreamProxyLog(ctx context.Context, w LineWriter) {
	streamCmd(ctx, w, "podman", "logs", "-f", "--tail", "40", "f5-license-proxy")
}

// ── k8s backend (in-cluster helm FLP) ────────────────────────────────────────

type k8sBackend struct{ ns string }

func (k *k8sBackend) Kind() string { return "cluster" }
func (k *k8sBackend) ProxyURL() string {
	// The chart's Service; reachable from a pod in the same cluster.
	return "https://f5-license-proxy." + k.ns + ".svc:8443/"
}

// container maps the podman container names to the k8s pod's containers. In the
// helm deployment the four run as containers in the f5-license-proxy pod(s).
func (k *k8sBackend) Container(ctx context.Context, name string) (Indicator, string) {
	out, err := run(ctx, "kubectl", "get", "pods", "-n", k.ns,
		"-o", "jsonpath={range .items[*].status.containerStatuses[*]}{.name}={.ready} {.state}{'\\n'}{end}")
	if err != nil || out == "" {
		return Unknown, "no pods"
	}
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, name+"=") {
			continue
		}
		if strings.Contains(line, "=true") && strings.Contains(line, "running") {
			return Up, "running"
		}
		if strings.Contains(line, "waiting") {
			return Pending, "waiting"
		}
		return Down, "not ready"
	}
	return Unknown, "not found"
}

func (k *k8sBackend) ProxyLog(ctx context.Context, n int) (string, error) {
	return run(ctx, "kubectl", "logs", "-n", k.ns, "-l", "app=f5-license-proxy",
		"-c", "f5-license-proxy", "--tail", itoa(n))
}

func (k *k8sBackend) StreamProxyLog(ctx context.Context, w LineWriter) {
	streamCmd(ctx, w, "kubectl", "logs", "-n", k.ns, "-l", "app=f5-license-proxy",
		"-c", "f5-license-proxy", "-f", "--tail", "40")
}

// streamCmd runs a `logs -f` command and forwards each stdout line to w until the
// context is cancelled (the SSE client disconnected).
func streamCmd(ctx context.Context, w LineWriter, name string, args ...string) {
	cmd := exec.CommandContext(ctx, name, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		w.WriteLine("error: " + err.Error())
		return
	}
	if err := cmd.Start(); err != nil {
		w.WriteLine("error: " + err.Error())
		return
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		w.WriteLine(sc.Text())
	}
	_ = cmd.Wait()
}

func itoa(n int) string {
	if n <= 0 {
		return "40"
	}
	b := []byte{}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
