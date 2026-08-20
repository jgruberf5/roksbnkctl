package config

import (
	"strings"
	"testing"
)

func genericWS(host, prefix string) *Workspace {
	return &Workspace{Registry: &RegistryCfg{
		Target:            "generic",
		GenericHost:       host,
		GenericRepoPrefix: prefix,
	}}
}

// #112. PR #110 taught the registry SUBCOMMANDS to distrust a record that
// describes a different mirror. The paths that act on the same record without
// asking — the tfvars render, the phase guard, the node CA trust — kept
// believing it. Those are the consequential ones: they redirect an install, or
// push a CA onto every node, for a mirror the workspace is not configured for.
func TestMirrorRecordMismatchSharedCheck(t *testing.T) {
	cases := []struct {
		name     string
		ws       *Workspace
		rec      *RegistryMirror
		mismatch bool
		mentions []string
	}{
		{
			name:     "a record for another target kind",
			ws:       genericWS("artifactory.example.com", "bnk-mirror"),
			rec:      &RegistryMirror{Target: "icr", Namespace: "bnk-mirror"},
			mismatch: true,
			mentions: []string{"icr", "generic"},
		},
		{
			name:     "a record for another repository",
			ws:       genericWS("artifactory.example.com", "docker-local"),
			rec:      &RegistryMirror{Target: "generic", Namespace: "bnk-mirror"},
			mismatch: true,
			mentions: []string{"bnk-mirror", "docker-local"},
		},
		{
			name:     "a record for another host",
			ws:       genericWS("artifactory.example.com", "bnk-mirror"),
			rec:      &RegistryMirror{Target: "generic", Namespace: "bnk-mirror", ImageHost: "harbor.example.com/bnk-mirror"},
			mismatch: true,
			mentions: []string{"harbor.example.com", "artifactory.example.com"},
		},
		{
			name: "the configured mirror",
			ws:   genericWS("artifactory.example.com", "bnk-mirror"),
			rec: &RegistryMirror{
				Target: "generic", Namespace: "bnk-mirror",
				ImageHost: "artifactory.example.com/bnk-mirror",
			},
		},
		{
			name: "a nil record",
			ws:   genericWS("h", "p"),
		},
		{
			// Cannot-tell must read as "trust the record": refusing because the
			// config is incomplete would break workspaces whose mirror is fine.
			name: "a config too incomplete to resolve",
			ws:   &Workspace{Registry: &RegistryCfg{Target: "generic"}}, // no host
			rec:  &RegistryMirror{Target: "generic", Namespace: "bnk-mirror"},
		},
		{
			name: "an icr region with no known registry host",
			ws:   &Workspace{Prefix: "p", IBMCloud: IBMCloudCfg{Region: "mars-north"}},
			rec:  &RegistryMirror{Target: "icr", Namespace: "p"},
		},
		{
			// The default target is icr, so a record with no Target recorded and
			// a resolvable icr config is consistent.
			name: "an icr record matching the derived host",
			ws:   &Workspace{Prefix: "bnkci", IBMCloud: IBMCloudCfg{Region: "us-south"}},
			rec:  &RegistryMirror{Target: "icr", Namespace: "bnkci", ImageHost: "us.icr.io/bnkci"},
		},
		{
			name:     "an icr record whose namespace drifted from the prefix",
			ws:       &Workspace{Prefix: "bnkci", IBMCloud: IBMCloudCfg{Region: "us-south"}},
			rec:      &RegistryMirror{Target: "icr", Namespace: "old-prefix"},
			mismatch: true,
			mentions: []string{"old-prefix", "bnkci"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			why := MirrorRecordMismatch(tc.ws, tc.rec, "")
			if tc.mismatch && why == "" {
				t.Fatal("expected a mismatch, got none — the record would be trusted")
			}
			if !tc.mismatch && why != "" {
				t.Fatalf("expected no mismatch, got: %s", why)
			}
			for _, m := range tc.mentions {
				if !strings.Contains(why, m) {
					t.Errorf("the reason should name %q, got: %s", m, why)
				}
			}
		})
	}
}

// The `--target` flag repoints the backend for a single invocation, so the
// check has to resolve against the override, not only the file.
func TestMirrorRecordMismatchHonoursTheTargetOverride(t *testing.T) {
	ws := genericWS("artifactory.example.com", "bnk-mirror")
	rec := &RegistryMirror{Target: "generic", Namespace: "bnk-mirror"}

	if why := MirrorRecordMismatch(ws, rec, ""); why != "" {
		t.Fatalf("without an override this record matches, got: %s", why)
	}
	why := MirrorRecordMismatch(ws, rec, "icr")
	if why == "" {
		t.Fatal("--target icr must not trust a record written for generic")
	}
	if !strings.Contains(why, "generic") || !strings.Contains(why, "icr") {
		t.Errorf("the reason should name both targets, got: %s", why)
	}
}

// The non-interactive paths cannot ask, so their error has to say what to run.
func TestMirrorRecordMismatchErrorIsActionable(t *testing.T) {
	ws := genericWS("artifactory.example.com", "docker-local")
	rec := &RegistryMirror{Target: "generic", Namespace: "bnk-mirror"}

	err := MirrorRecordMismatchError("prod", ws, rec)
	if err == nil {
		t.Fatal("a mismatched record must produce an error on the non-interactive paths")
	}
	for _, want := range []string{"registry replicate", "registry adopt", "-w prod", "bnk-mirror", "docker-local"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should mention %q, got:\n%s", want, err)
		}
	}

	matching := &RegistryMirror{Target: "generic", Namespace: "docker-local"}
	if err := MirrorRecordMismatchError("prod", ws, matching); err != nil {
		t.Errorf("a matching record must not error, got: %v", err)
	}
}

// HostPath has to produce exactly what a built target records as ImageHost, or
// the host comparison reports a mismatch on every correctly-recorded mirror.
func TestMirrorIdentityHostPath(t *testing.T) {
	for _, tc := range []struct {
		id   MirrorIdentity
		want string
	}{
		{MirrorIdentity{Host: "h.example.com", Namespace: "ns"}, "h.example.com/ns"},
		{MirrorIdentity{Host: "h.example.com"}, "h.example.com"},
		{MirrorIdentity{Host: "h.example.com:5000", Namespace: "a/b"}, "h.example.com:5000/a/b"},
	} {
		if got := tc.id.HostPath(); got != tc.want {
			t.Errorf("HostPath() = %q, want %q", got, tc.want)
		}
	}
}

// `registry replicate --target generic` is a supported way to mirror without
// putting `target: generic` in config.yaml — scripts/demos/far-replication-demo
// and shared-licensing-cli-demo both do exactly that, passing the flag and never
// setting ROKSBNKCTL_REGISTRY_TARGET.
//
// Those workspaces end up holding a record for a generic mirror while their
// config states no target at all. Reading the unstated "icr" default as an
// assertion would call that a mismatch and refuse every subsequent `bnk up` —
// breaking two shipped demos for a mirror that is perfectly correct.
func TestAnUnstatedTargetIsADefaultNotAnAssertion(t *testing.T) {
	// Exactly what the demos leave behind: generic_host + prefix configured,
	// `target:` never set, record written under `--target generic`.
	ws := &Workspace{Registry: &RegistryCfg{
		GenericHost:       "far.example.com",
		GenericRepoPrefix: "bnk-mirror",
	}}
	rec := &RegistryMirror{
		Target:    "generic",
		Namespace: "bnk-mirror",
		ChartHost: "far.example.com/bnk-mirror",
		ImageHost: "far.example.com/bnk-mirror",
	}

	if why := MirrorRecordMismatch(ws, rec, ""); why != "" {
		t.Fatalf("a flag-driven generic mirror must survive a later `bnk up`, got: %s", why)
	}
	if err := MirrorRecordMismatchError("demo", ws, rec); err != nil {
		t.Fatalf("the demo path must not be refused: %v", err)
	}

	// The real drift is still caught in the same configuration: same unstated
	// target, record written for a different repository.
	stale := &RegistryMirror{
		Target:    "generic",
		Namespace: "docker-local",
		ImageHost: "far.example.com/docker-local",
	}
	why := MirrorRecordMismatch(ws, stale, "")
	if why == "" {
		t.Fatal("an unstated target must not disable the repository check")
	}
	if !strings.Contains(why, "docker-local") || !strings.Contains(why, "bnk-mirror") {
		t.Errorf("the reason should name both repositories, got: %s", why)
	}
}

// Stating the target explicitly keeps it authoritative: config that says icr
// must not silently accept a record written for generic.
func TestAStatedTargetIsAuthoritative(t *testing.T) {
	ws := &Workspace{
		Prefix:   "bnkci",
		IBMCloud: IBMCloudCfg{Region: "us-south"},
		Registry: &RegistryCfg{Target: "icr", GenericHost: "far.example.com"},
	}
	rec := &RegistryMirror{Target: "generic", Namespace: "bnkci"}
	why := MirrorRecordMismatch(ws, rec, "")
	if why == "" {
		t.Fatal("config stating target: icr must not trust a record written for generic")
	}
	if !strings.Contains(why, "generic") || !strings.Contains(why, "icr") {
		t.Errorf("the reason should name both targets, got: %s", why)
	}
}
