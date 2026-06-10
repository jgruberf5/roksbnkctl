package test

import (
	"strings"
	"testing"
)

// A representative h2load report (HTTP/1.1, cps-style run). Whitespace
// matches h2load's real output closely enough to exercise the regexes.
const h2loadSample = `starting benchmark...
spawning thread #0: 50 total client(s). 10000 total requests
Application protocol: http/1.1
progress: 10% done
finished in 1.23s, 8130.08 req/s, 10.30MB/s
requests: 10000 total, 10000 started, 10000 done, 9998 succeeded, 2 failed, 0 errored, 0 timeout
status codes: 9998 2xx, 0 3xx, 0 4xx, 2 5xx
traffic: 12.34MB (12940000) total, 1.85MB (1940000) headers, 9.54MB (10000000) data
                     min         max         mean         sd        +/- sd
time for request:      102us       4.51ms       1.21ms       512us    89.12%
time for connect:       91us       1.02ms       512us       298us    70.00%
req/s           :      8130.08     8130.08     8130.08        0.00   100.00%
`

func TestParseH2load(t *testing.T) {
	r, err := ParseH2load(h2loadSample)
	if err != nil {
		t.Fatalf("ParseH2load: %v", err)
	}
	if r.ReqPerSec != 8130.08 {
		t.Errorf("ReqPerSec = %v, want 8130.08", r.ReqPerSec)
	}
	if r.BytesPerSec != 10.30*1e6 {
		t.Errorf("BytesPerSec = %v, want %v", r.BytesPerSec, 10.30*1e6)
	}
	if r.TotalReqs != 10000 || r.Succeeded != 9998 || r.Failed != 2 {
		t.Errorf("requests parse = total %d succ %d fail %d", r.TotalReqs, r.Succeeded, r.Failed)
	}
	if r.Status2xx != 9998 || r.Status5xx != 2 {
		t.Errorf("status parse = 2xx %d 5xx %d", r.Status2xx, r.Status5xx)
	}
	// mean 1.21ms → 0.00121s; min 102us → 0.000102s; max 4.51ms.
	if got := r.ReqTimeMeanS; got < 0.00120 || got > 0.00122 {
		t.Errorf("ReqTimeMeanS = %v, want ~0.00121", got)
	}
	if got := r.ReqTimeMinS; got < 0.000101 || got > 0.000103 {
		t.Errorf("ReqTimeMinS = %v, want ~0.000102", got)
	}
}

func TestParseH2load_NoSummary(t *testing.T) {
	if _, err := ParseH2load("spawning thread #0\nprogress: 10% done\n"); err == nil {
		t.Fatal("expected error when the finished-in summary is absent")
	}
}

func TestL7Args_ModePresets(t *testing.T) {
	cases := []struct {
		name     string
		opts     L7Options
		wantHas  []string
		wantMiss []string
	}{
		{
			name:    "cps forces http1 and one-stream",
			opts:    L7Options{URL: "http://vip/128", Mode: L7ModeCPS},
			wantHas: []string{"--h1", "-m", "1", "http://vip/128"},
		},
		{
			name:     "tps over h2 uses multiplexed streams",
			opts:     L7Options{URL: "http://vip/128", Mode: L7ModeTPS},
			wantHas:  []string{"-m", "100"},
			wantMiss: []string{"--h1"},
		},
		{
			name:     "duration mode emits -D and not -n",
			opts:     L7Options{URL: "https://vip/512k", Mode: L7ModeThroughput, Duration: 30},
			wantHas:  []string{"-D", "30", "https://vip/512k"},
			wantMiss: []string{"-n"},
		},
		{
			name:    "explicit knobs win over presets",
			opts:    L7Options{URL: "http://vip/128", Mode: L7ModeCPS, Clients: 7, Requests: 99},
			wantHas: []string{"-c", "7", "-n", "99"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args, err := L7Args(tc.opts)
			if err != nil {
				t.Fatalf("L7Args: %v", err)
			}
			joined := " " + strings.Join(args, " ") + " "
			for _, w := range tc.wantHas {
				if !strings.Contains(joined, " "+w+" ") {
					t.Errorf("args %v missing %q", args, w)
				}
			}
			for _, w := range tc.wantMiss {
				if strings.Contains(joined, " "+w+" ") {
					t.Errorf("args %v should not contain %q", args, w)
				}
			}
		})
	}
}

func TestL7Args_Validation(t *testing.T) {
	if _, err := L7Args(L7Options{Mode: L7ModeCPS}); err == nil {
		t.Error("expected error on empty URL")
	}
	if _, err := L7Args(L7Options{URL: "http://x"}); err == nil {
		t.Error("expected error on empty mode")
	}
	if _, err := L7Args(L7Options{URL: "http://x", Mode: "bogus"}); err == nil {
		t.Error("expected error on unknown mode")
	}
}
