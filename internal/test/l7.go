package test

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// L7Mode is one of the perf plan's three Ingress-L7 measurement shapes,
// expressed in h2load-native terms. The mode is informational (it lands
// in the report) and also seeds sensible flag defaults when a cell
// leaves the low-level knobs unset (see L7Args).
//
//   - cps         — connection-oriented: HTTP/1.1, one request per
//     connection, so the rate is dominated by connect cost.
//     The plan's "CPS @ 1 request/connection, 128 B".
//   - tps         — transaction-oriented: many requests reusing kept-alive
//     connections. The plan's "TPS @ 100 requests/connection".
//   - throughput  — payload-oriented: a large response body, rate measured
//     in bytes/s. The plan's "throughput @ 512 KB".
type L7Mode string

const (
	L7ModeCPS        L7Mode = "cps"
	L7ModeTPS        L7Mode = "tps"
	L7ModeThroughput L7Mode = "throughput"
)

// L7Options configures one h2load run. URL carries the scheme, so an
// https:// URL exercises the TLS-terminate-at-TMM path and an http://
// URL the cleartext HTTPRoute path — no separate TLS toggle needed
// (h2load speaks TLS off the scheme and does not hard-fail on a
// self-signed server cert, which is what a TMM-internal terminate
// presents).
type L7Options struct {
	URL  string // full target URL; scheme (http/https) selects TLS
	Mode L7Mode // cps | tps | throughput (presets + report label)

	// Requests is h2load -n (total requests). When 0, Duration drives a
	// time-bounded run via -D instead.
	Requests int
	// Duration is h2load -D (seconds) — used only when Requests == 0.
	Duration int
	// Clients is h2load -c (concurrent connections).
	Clients int
	// Streams is h2load -m (max concurrent streams per connection). For
	// HTTP/1.1 this is pinned to 1 by h2load regardless; meaningful on h2.
	Streams int
	// Threads is h2load -t (worker threads); 0 leaves h2load's default.
	Threads int
	// HTTP1 forces HTTP/1.1 (h2load --h1). The cps preset sets this.
	HTTP1 bool
}

// L7Args builds the h2load argv (sans argv[0]) for opts, filling
// per-mode defaults for any knob the caller left at zero. The defaults
// mirror the perf plan's intent so a cell can be as terse as
// {mode: cps} and still produce a meaningful run.
func L7Args(opts L7Options) ([]string, error) {
	if strings.TrimSpace(opts.URL) == "" {
		return nil, fmt.Errorf("l7: URL is required")
	}

	clients := opts.Clients
	streams := opts.Streams
	requests := opts.Requests
	http1 := opts.HTTP1

	// Per-mode seeding. Only fills zero-valued knobs — an explicit value
	// in the cell always wins.
	switch opts.Mode {
	case L7ModeCPS:
		// Connection-bound: HTTP/1.1, one stream, one request per
		// connection. With requests == clients each connection serves a
		// single request, so the measured req/s is effectively conn/s.
		http1 = true
		if streams == 0 {
			streams = 1
		}
		if clients == 0 {
			clients = 50
		}
		if requests == 0 && opts.Duration == 0 {
			requests = clients * 200
		}
	case L7ModeTPS:
		// Transaction-bound: reuse connections, many requests each.
		if clients == 0 {
			clients = 50
		}
		if streams == 0 {
			if http1 {
				streams = 1
			} else {
				streams = 100
			}
		}
		if requests == 0 && opts.Duration == 0 {
			requests = clients * 2000
		}
	case L7ModeThroughput:
		// Payload-bound: a handful of connections pulling a large body.
		if clients == 0 {
			clients = 8
		}
		if streams == 0 {
			streams = 1
		}
		if requests == 0 && opts.Duration == 0 {
			requests = clients * 200
		}
	case "":
		return nil, fmt.Errorf("l7: mode is required (cps|tps|throughput)")
	default:
		return nil, fmt.Errorf("l7: unknown mode %q (want cps|tps|throughput)", opts.Mode)
	}

	if clients <= 0 {
		clients = 1
	}
	if streams <= 0 {
		streams = 1
	}

	args := []string{"-c", strconv.Itoa(clients), "-m", strconv.Itoa(streams)}
	if opts.Threads > 0 {
		args = append(args, "-t", strconv.Itoa(opts.Threads))
	}
	if http1 {
		args = append(args, "--h1")
	}
	switch {
	case requests > 0:
		args = append(args, "-n", strconv.Itoa(requests))
	case opts.Duration > 0:
		args = append(args, "-D", strconv.Itoa(opts.Duration))
	default:
		return nil, fmt.Errorf("l7: need either requests (-n) or duration (-D)")
	}
	args = append(args, opts.URL)
	return args, nil
}

// L7Result is the parsed shape of an h2load run. Fields map 1:1 to what
// h2load actually prints — note h2load's default report gives request-
// time min/max/mean/sd, NOT p50/p95/p99 (those need --log-file
// post-processing), so we surface the honest min/max/mean rather than
// fabricate percentiles.
type L7Result struct {
	ReqPerSec    float64 // "finished in …, N req/s, …"
	BytesPerSec  float64 // the MB/s figure, normalised to bytes/s
	TotalReqs    int
	Succeeded    int
	Failed       int
	Errored      int
	Timeout      int
	Status2xx    int
	Status3xx    int
	Status4xx    int
	Status5xx    int
	ReqTimeMeanS float64 // "time for request: … mean …" normalised to seconds
	ReqTimeMinS  float64
	ReqTimeMaxS  float64
}

var (
	reFinished  = regexp.MustCompile(`finished in [^,]+,\s*([0-9.]+)\s*req/s,\s*([0-9.]+)\s*([KMG]?B)/s`)
	reRequests  = regexp.MustCompile(`requests:\s*(\d+)\s*total,\s*\d+\s*started,\s*\d+\s*done,\s*(\d+)\s*succeeded,\s*(\d+)\s*failed,\s*(\d+)\s*errored,\s*(\d+)\s*timeout`)
	reStatus    = regexp.MustCompile(`status codes:\s*(\d+)\s*2xx,\s*(\d+)\s*3xx,\s*(\d+)\s*4xx,\s*(\d+)\s*5xx`)
	reReqTime   = regexp.MustCompile(`time for request:\s*([0-9.]+)([a-z]+)\s+([0-9.]+)([a-z]+)\s+([0-9.]+)([a-z]+)`)
	errNoH2load = fmt.Errorf("no h2load summary found in output")
)

// ParseH2load turns h2load's text report into an L7Result. It is
// tolerant of the lines it doesn't recognise (h2load's banner, progress
// dots, traffic line) and only hard-fails when the mandatory "finished
// in …" summary line is absent — that's the signal h2load didn't
// actually complete a run.
func ParseH2load(out string) (L7Result, error) {
	var r L7Result

	m := reFinished.FindStringSubmatch(out)
	if m == nil {
		return r, errNoH2load
	}
	r.ReqPerSec, _ = strconv.ParseFloat(m[1], 64)
	val, _ := strconv.ParseFloat(m[2], 64)
	r.BytesPerSec = val * byteUnitScale(m[3])

	if m := reRequests.FindStringSubmatch(out); m != nil {
		r.TotalReqs = atoi(m[1])
		r.Succeeded = atoi(m[2])
		r.Failed = atoi(m[3])
		r.Errored = atoi(m[4])
		r.Timeout = atoi(m[5])
	}
	if m := reStatus.FindStringSubmatch(out); m != nil {
		r.Status2xx = atoi(m[1])
		r.Status3xx = atoi(m[2])
		r.Status4xx = atoi(m[3])
		r.Status5xx = atoi(m[4])
	}
	if m := reReqTime.FindStringSubmatch(out); m != nil {
		r.ReqTimeMinS = durToSeconds(m[1], m[2])
		r.ReqTimeMaxS = durToSeconds(m[3], m[4])
		r.ReqTimeMeanS = durToSeconds(m[5], m[6])
	}
	return r, nil
}

// Extra renders an L7Result as the ProbeResult.Extra map the matrix
// report carries. Keys are stable (part of roksbnkctl.v1's additive
// surface).
func (r L7Result) Extra() map[string]any {
	return map[string]any{
		"req_per_sec":      r.ReqPerSec,
		"throughput_mbps":  r.BytesPerSec * 8 / 1e6, // bytes/s → Mbit/s
		"bytes_per_sec":    r.BytesPerSec,
		"requests_total":   r.TotalReqs,
		"succeeded":        r.Succeeded,
		"failed":           r.Failed,
		"errored":          r.Errored,
		"timeout":          r.Timeout,
		"status_2xx":       r.Status2xx,
		"status_4xx":       r.Status4xx,
		"status_5xx":       r.Status5xx,
		"req_time_mean_ms": r.ReqTimeMeanS * 1e3,
		"req_time_min_ms":  r.ReqTimeMinS * 1e3,
		"req_time_max_ms":  r.ReqTimeMaxS * 1e3,
	}
}

func byteUnitScale(unit string) float64 {
	switch unit {
	case "GB":
		return 1e9
	case "MB":
		return 1e6
	case "KB":
		return 1e3
	default: // "B"
		return 1
	}
}

// durToSeconds normalises an h2load time-with-unit (e.g. "1.21", "ms")
// to seconds. h2load uses ns/us/µs/ms/s.
func durToSeconds(val, unit string) float64 {
	f, _ := strconv.ParseFloat(val, 64)
	switch unit {
	case "ns":
		return f / 1e9
	case "us", "µs":
		return f / 1e6
	case "ms":
		return f / 1e3
	case "s", "":
		return f
	default:
		return f
	}
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
