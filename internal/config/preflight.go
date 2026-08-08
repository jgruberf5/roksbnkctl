package config

import "time"

// Defaults for the reachability gate.
//
// 180s comfortably exceeds the Transit Gateway route-programming delay observed in
// issue #57 (a probe 73s after attach failed; the path was healthy minutes later)
// while still failing a genuinely broken path far sooner than the ImagePullBackOff +
// helm-timeout route this gate replaces.
//
// The readiness wait has to be the larger of the two: it is waiting FOR the probe, so
// anything less would give up while the probe is still legitimately retrying.
const (
	DefaultReachabilityRetrySeconds   = 180
	DefaultReachabilityTimeoutSeconds = 480
)

// ReachabilityRetrySeconds is the configured per-target retry budget, or the default.
// A configured 0 is honoured — it means "one shot", which is a legitimate choice for
// a static environment where a failure is never a race.
func (w *Workspace) ReachabilityRetrySeconds() int {
	if w != nil && w.BNK.Preflight != nil && w.BNK.Preflight.ReachabilityRetrySeconds != nil {
		if v := *w.BNK.Preflight.ReachabilityRetrySeconds; v >= 0 {
			return v
		}
	}
	return DefaultReachabilityRetrySeconds
}

// ReachabilityTimeout is how long to wait for every node to report.
//
// It is clamped to stay strictly greater than the retry budget. Someone who raises
// the budget without raising the timeout has expressed a contradiction — "keep
// retrying for five minutes, but give up waiting after three" — and the failure that
// produces is the confusing kind: a timeout that looks like the network but is really
// the config. Rather than fail their run, take the intent (retry that long) and give
// the wait enough room, with a margin for the pod to start and the CA to install.
func (w *Workspace) ReachabilityTimeout() time.Duration {
	secs := DefaultReachabilityTimeoutSeconds
	if w != nil && w.BNK.Preflight != nil && w.BNK.Preflight.ReachabilityTimeoutSeconds != nil {
		if v := *w.BNK.Preflight.ReachabilityTimeoutSeconds; v > 0 {
			secs = v
		}
	}
	if min := w.ReachabilityRetrySeconds() + 120; secs < min {
		secs = min
	}
	return time.Duration(secs) * time.Second
}
