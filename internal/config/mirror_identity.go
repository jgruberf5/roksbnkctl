package config

import (
	"fmt"
	"strings"
)

// ICRHostForRegion maps an IBM Cloud region to its regional IBM Container
// Registry host, for target=icr when registry.icr_host is not set explicitly.
var ICRHostForRegion = map[string]string{
	"us-south": "us.icr.io", "us-east": "us.icr.io",
	"eu-de": "de.icr.io", "eu-gb": "uk.icr.io", "eu-es": "es.icr.io",
	"jp-tok": "jp.icr.io", "jp-osa": "jp2.icr.io",
	"au-syd": "au.icr.io", "ca-tor": "ca.icr.io", "br-sao": "br.icr.io",
}

// MirrorIdentity is the host+namespace a workspace's registry config points at:
// the identity half of a mirror target, with none of the credentials. It is
// resolved from config alone — no network, no keychain — so every consumer of a
// recorded mirror can check that the record still describes the configured
// mirror, including the ones that run before any client exists.
type MirrorIdentity struct {
	// Target is the backend kind: "icr" or "generic".
	Target string
	// Host is the registry host[:port].
	Host string
	// Namespace is the repository prefix artifacts nest under. Legitimately
	// empty for a generic target pushing to the registry root.
	Namespace string
}

// HostPath is "<host>/<namespace>", or "<host>" when Namespace is empty — the
// same value a built target reports as its image/chart host path, and the same
// value recorded as RegistryMirror.ImageHost.
func (m MirrorIdentity) HostPath() string {
	if m.Namespace == "" {
		return m.Host
	}
	return m.Host + "/" + m.Namespace
}

// MirrorTargetKind resolves the configured backend: override (the `--target`
// flag, "" when unset) > registry.target > "icr", the default.
func MirrorTargetKind(ws *Workspace, override string) string {
	kind, _ := configuredTargetKind(ws, override)
	return kind
}

// configuredTargetKind additionally reports whether the kind was actually
// STATED — by the flag or by registry.target — as opposed to falling through to
// the "icr" default.
//
// The distinction matters when judging a record. `registry replicate --target
// generic` is a supported way to mirror without putting `target: generic` in
// config.yaml (two shipped demos do exactly that), so a workspace can hold a
// record for a generic mirror while its config states no target at all.
// Treating the unstated default as an assertion would call that a mismatch and
// refuse every subsequent `bnk up`.
func configuredTargetKind(ws *Workspace, override string) (string, bool) {
	if override != "" {
		return override, true
	}
	if ws != nil && ws.Registry != nil && ws.Registry.Target != "" {
		return ws.Registry.Target, true
	}
	return "icr", false
}

// ResolveMirrorIdentity resolves the mirror the workspace config points at.
//
// ok is false when the identity cannot be determined from config — an unset
// generic_host, an ICR region with no known registry host, a target kind this
// build does not know. Callers must treat that as "cannot tell" and leave the
// recorded mirror alone: refusing to use a record because the configured target
// is incomplete would break workspaces whose mirror is fine.
func ResolveMirrorIdentity(ws *Workspace, override string) (MirrorIdentity, bool) {
	if ws == nil {
		return MirrorIdentity{}, false
	}
	reg := ws.Registry
	if reg == nil {
		reg = &RegistryCfg{}
	}
	kind := MirrorTargetKind(ws, override)
	switch kind {
	case "icr":
		host := reg.ICRHost
		if host == "" {
			h, known := ICRHostForRegion[ws.IBMCloud.Region]
			if !known {
				return MirrorIdentity{}, false
			}
			host = h
		}
		ns := reg.ICRNamespace
		if ns == "" {
			ns = ws.Prefix
		}
		if ns == "" {
			return MirrorIdentity{}, false
		}
		return MirrorIdentity{Target: kind, Host: host, Namespace: ns}, true
	case "generic":
		if reg.GenericHost == "" {
			return MirrorIdentity{}, false
		}
		return MirrorIdentity{Target: kind, Host: reg.GenericHost, Namespace: reg.GenericRepoPrefix}, true
	default:
		return MirrorIdentity{}, false
	}
}

// MirrorRecordMismatch reports why rec does not describe the mirror ws is
// configured for, or "" when the record is consistent with the config — which
// includes the case where the config is too incomplete to tell.
//
// A workspace records what it last replicated. That record can outlive the
// mirror it describes: the registry gets rebuilt, emptied, or the config is
// repointed at a different repository. Nothing re-probes on read, so a stale
// record is believed. Every consumer that acts on a recorded mirror — the
// install redirect, the node CA trust, the phase guard, the registry
// subcommands — checks this first so it acts on the configured mirror or not at
// all.
//
// override carries the `--target` flag for the registry subcommands; pass "" on
// paths that have no such flag.
func MirrorRecordMismatch(ws *Workspace, rec *RegistryMirror, override string) string {
	if rec == nil || ws == nil {
		return ""
	}
	kind, stated := configuredTargetKind(ws, override)
	if stated && rec.Target != "" && rec.Target != kind {
		return fmt.Sprintf("it was written for target %q, the configured target is %q", rec.Target, kind)
	}
	// With no target stated anywhere, the "icr" default is a fallback and not a
	// claim about this workspace's mirror — so check the record's host and
	// repository against the config read through the record's OWN kind. A record
	// for a mirror the config cannot describe at all resolves to "cannot tell"
	// below and is left alone.
	resolveAs := override
	if !stated && rec.Target != "" {
		resolveAs = rec.Target
	}
	id, ok := ResolveMirrorIdentity(ws, resolveAs)
	if !ok {
		return "" // cannot resolve the configured mirror — say nothing rather than guess
	}
	if rec.Namespace != "" && id.Namespace != "" && rec.Namespace != id.Namespace {
		return fmt.Sprintf("it was written for repository %q, the configured repository is %q", rec.Namespace, id.Namespace)
	}
	if hp := id.HostPath(); rec.ImageHost != "" && hp != "" && rec.ImageHost != hp {
		return fmt.Sprintf("it was written for host %q, the configured host is %q", rec.ImageHost, hp)
	}
	return ""
}

// MirrorRecordMismatchError wraps MirrorRecordMismatch in the actionable error
// the non-interactive paths return: they cannot ask, and proceeding would point
// the install (or the node trust store) at a mirror the workspace is not
// configured for.
func MirrorRecordMismatchError(workspace string, ws *Workspace, rec *RegistryMirror) error {
	why := MirrorRecordMismatch(ws, rec, "")
	if why == "" {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "the recorded registry mirror does not describe the configured mirror: %s.\n", why)
	b.WriteString("  Acting on it would use a mirror this workspace is not configured for.\n")
	fmt.Fprintf(&b, "  Re-record it:   roksbnkctl -w %s registry replicate   (needs the FAR source)\n", workspace)
	fmt.Fprintf(&b, "  Already filled: roksbnkctl -w %s registry adopt       (no source access needed)", workspace)
	return fmt.Errorf("%s", b.String())
}
