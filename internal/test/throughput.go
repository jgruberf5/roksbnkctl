package test

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"time"
)

// ThroughputOptions configures `roksbnkctl test throughput`.
type ThroughputOptions struct {
	Mode     string // north-south | east-west (informational; affects how Endpoint was resolved)
	Endpoint string // host or IP the iperf3 client connects to (port 5201 implied)
	Duration int    // seconds; iperf3 default is 10 if 0
	Streams  int    // parallel streams; iperf3 default is 1 if 0

	// Length is iperf3's -l buffer/block size (e.g. "128", "512K"). It is
	// the L4 analog of the perf plan's HTTP payload axis: a tiny block
	// ("128") drives the syscall-bound small-message regime, a large one
	// ("512K") the bulk-copy regime. Empty leaves iperf3's default (128K).
	Length string
	// Bytes is iperf3's -n: transfer a fixed number of bytes instead of
	// running for Duration seconds (e.g. "512K", "1G"). When set, -n wins
	// over -t on the iperf3 side. Empty leaves the time-bounded run.
	Bytes string
	// Port is the iperf3 server port; 0 implies iperf3's default 5201.
	Port int
}

// Iperf3Args builds the iperf3 *client* argv (sans the "iperf3" argv[0])
// for opts. Shared by the local engine here and the k8s / ssh backend
// dispatchers in internal/cli so every backend speaks the same flags —
// including the -l / -n content-size knobs the matrix relies on.
func Iperf3Args(opts ThroughputOptions) []string {
	args := []string{"-c", opts.Endpoint, "-J"} // -J = JSON output
	if opts.Port > 0 {
		args = append(args, "-p", strconv.Itoa(opts.Port))
	}
	// -n (fixed bytes) is mutually exclusive with -t (time) in iperf3;
	// when both are asked for, -n wins (it's the more specific request).
	switch {
	case opts.Bytes != "":
		args = append(args, "-n", opts.Bytes)
	case opts.Duration > 0:
		args = append(args, "-t", strconv.Itoa(opts.Duration))
	}
	if opts.Streams > 0 {
		args = append(args, "-P", strconv.Itoa(opts.Streams))
	}
	if opts.Length != "" {
		args = append(args, "-l", opts.Length)
	}
	return args
}

// RunThroughput runs an iperf3 client against opts.Endpoint and returns
// a SuiteRun with measured throughput in Extra.
//
// Pre-conditions: iperf3 binary on PATH; an iperf3 server reachable at
// opts.Endpoint:5201. The CLI layer is responsible for deploying that
// server (via internal/k8s) and resolving its endpoint before calling.
func RunThroughput(ctx context.Context, opts ThroughputOptions) SuiteRun {
	start := time.Now()
	probe := iperf3Probe(ctx, opts)
	probes := []ProbeResult{probe}
	return SuiteRun{
		Schema:     SchemaVersion,
		Command:    "test",
		Suite:      "throughput",
		Timestamp:  start,
		DurationMS: time.Since(start).Milliseconds(),
		Results:    probes,
		Overall:    Aggregate(probes),
	}
}

func iperf3Probe(ctx context.Context, opts ThroughputOptions) ProbeResult {
	name := fmt.Sprintf("iperf3 %s → %s", opts.Mode, opts.Endpoint)
	p := ProbeResult{Suite: "throughput", Name: name, Status: StatusPass}

	if opts.Endpoint == "" {
		p.Status = StatusFail
		p.Detail = "endpoint is empty"
		return p
	}
	if _, err := exec.LookPath("iperf3"); err != nil {
		p.Status = StatusFail
		p.Detail = "iperf3 not found on PATH (install iperf3 to run throughput tests)"
		return p
	}

	args := Iperf3Args(opts)

	start := time.Now()
	out, err := exec.CommandContext(ctx, "iperf3", args...).Output()
	p.DurationMS = time.Since(start).Milliseconds()

	if err != nil {
		p.Status = StatusFail
		p.Detail = fmt.Sprintf("iperf3 failed: %v", err)
		return p
	}

	gbps, retransmits, perr := parseIperf3JSON(out)
	if perr != nil {
		p.Status = StatusFail
		p.Detail = fmt.Sprintf("parsing iperf3 output: %v", perr)
		return p
	}

	p.Detail = fmt.Sprintf("%.2f Gbit/s (%d retransmits)", gbps, retransmits)
	p.Extra = map[string]any{
		"throughput_gbps": gbps,
		"retransmits":     retransmits,
		"endpoint":        opts.Endpoint,
		"mode":            opts.Mode,
		"duration_s":      opts.Duration,
		"streams":         opts.Streams,
	}
	if opts.Length != "" {
		p.Extra["length"] = opts.Length
	}
	if opts.Bytes != "" {
		p.Extra["bytes"] = opts.Bytes
	}
	return p
}

// ParseIperf3JSON exposes the iperf3 -J summary parse to other packages
// (the matrix runner dispatches the client itself, then folds the parsed
// receiver-side Gbit/s + retransmits into the cell's result).
func ParseIperf3JSON(b []byte) (gbps float64, retransmits int, err error) {
	return parseIperf3JSON(b)
}

// parseIperf3JSON pulls the throughput summary out of iperf3's -J output.
// We use end.sum_received because that's what the receiver actually got
// after retransmits; sum_sent is the optimistic sender-side number.
func parseIperf3JSON(b []byte) (gbps float64, retransmits int, err error) {
	var r struct {
		End struct {
			SumReceived struct {
				BitsPerSecond float64 `json:"bits_per_second"`
				Retransmits   int     `json:"retransmits"`
			} `json:"sum_received"`
		} `json:"end"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return 0, 0, err
	}
	return r.End.SumReceived.BitsPerSecond / 1e9, r.End.SumReceived.Retransmits, nil
}
