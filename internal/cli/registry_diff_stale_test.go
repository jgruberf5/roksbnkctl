package cli

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// #109. A workspace records what it last replicated. That record can describe a
// registry that has since been rebuilt, emptied, or swapped for another — and
// `diff` never probes, so it answered "in sync" against an EMPTY registry while
// `verify` correctly reported all 89 artifacts missing.
//
// The mismatch check is what makes the record trustworthy: if it does not
// describe the configured target, it says nothing about it.
func TestMirrorRecordMismatch(t *testing.T) {
	genericWS := func(host, prefix string) *config.Workspace {
		return &config.Workspace{
			Registry: &config.RegistryCfg{
				Target:            "generic",
				GenericHost:       host,
				GenericRepoPrefix: prefix,
			},
		}
	}

	t.Run("a different target kind is a mismatch", func(t *testing.T) {
		ws := genericWS("artifactory.example.com", "bnk-mirror")
		rec := &config.RegistryMirror{Target: "icr", Namespace: "bnk-mirror"}
		why := mirrorRecordMismatch(context.Background(), "ws", ws, rec)
		if why == "" {
			t.Fatal("a record written for icr must not be trusted for a generic target")
		}
		if !strings.Contains(why, "icr") || !strings.Contains(why, "generic") {
			t.Errorf("the reason should name both targets, got: %s", why)
		}
	})

	t.Run("a different repository is a mismatch", func(t *testing.T) {
		// The exact shape observed: recorded against bnk-mirror, configured for
		// docker-local, registry empty.
		ws := genericWS("artifactory.example.com", "docker-local")
		rec := &config.RegistryMirror{Target: "generic", Namespace: "bnk-mirror"}
		why := mirrorRecordMismatch(context.Background(), "ws", ws, rec)
		if why == "" {
			t.Fatal("a record written for another repository must not be trusted")
		}
		if !strings.Contains(why, "bnk-mirror") || !strings.Contains(why, "docker-local") {
			t.Errorf("the reason should name both repositories, got: %s", why)
		}
	})

	t.Run("the same target is not a mismatch", func(t *testing.T) {
		ws := genericWS("artifactory.example.com", "bnk-mirror")
		rec := &config.RegistryMirror{Target: "generic", Namespace: "bnk-mirror"}
		if why := mirrorRecordMismatch(context.Background(), "ws", ws, rec); why != "" {
			t.Errorf("a record for the configured target must be trusted, got: %s", why)
		}
	})

	t.Run("a nil record is not a mismatch", func(t *testing.T) {
		if why := mirrorRecordMismatch(context.Background(), "ws", genericWS("h", "p"), nil); why != "" {
			t.Errorf("nil record should be silent, got: %s", why)
		}
	})

	// Cannot-tell must behave as "trust the record". Discarding it because the
	// target will not build would turn a diff into a full re-replicate for a
	// reason unrelated to the mirror's contents.
	t.Run("an unresolvable target trusts the record", func(t *testing.T) {
		ws := &config.Workspace{Registry: &config.RegistryCfg{Target: "generic"}} // no host
		rec := &config.RegistryMirror{Target: "generic", Namespace: "bnk-mirror"}
		if why := mirrorRecordMismatch(context.Background(), "ws", ws, rec); why != "" {
			t.Errorf("an unresolvable target should say nothing, got: %s", why)
		}
	})
}

// The destructive commands REFUSE rather than shrug.
//
// delete takes its artifact list from the RECORD and removes it from the
// CONFIGURED target. With a record describing another mirror that deletes one
// registry's contents out of a different one — and delete's confirmation prompt
// names the RECORD's host, so it would state the wrong destination while doing
// it. diff can afford to discard a mismatched record and report everything
// missing; an unrecoverable delete cannot.
//
// This asserts the wiring, not just the helper: a guard that exists but is never
// called is the shape that let #100 ship.
func TestDestructiveRegistryCommandsGuardAgainstAStaleRecord(t *testing.T) {
	src, err := os.ReadFile("registry.go")
	if err != nil {
		t.Fatalf("reading registry.go: %v", err)
	}
	body := string(src)

	for _, fn := range []string{"runRegistryDelete", "runRegistryPrune"} {
		start := strings.Index(body, "func "+fn)
		if start < 0 {
			t.Fatalf("%s not found — this test can no longer detect the gap", fn)
		}
		end := strings.Index(body[start:], "\n}\n")
		if end < 0 {
			t.Fatalf("could not delimit %s", fn)
		}
		fnBody := body[start : start+end]

		if !strings.Contains(fnBody, "mirrorRecordMismatch") {
			t.Errorf("%s does not check mirrorRecordMismatch: it would delete the recorded "+
				"mirror's artifact list from the CONFIGURED target", fn)
			continue
		}
		if !strings.Contains(fnBody, "refusing to") {
			t.Errorf("%s checks the record but does not REFUSE — a destructive command must "+
				"not proceed on a record for a different mirror", fn)
		}
		// delete's prompt names the record's host, so the refusal has to come first.
		if fn == "runRegistryDelete" {
			guard := strings.Index(fnBody, "mirrorRecordMismatch")
			prompt := strings.Index(fnBody, "promptYesNo")
			if prompt >= 0 && guard > prompt {
				t.Errorf("%s prompts before guarding — the prompt names the RECORD's host, "+
					"so it would ask about the wrong registry", fn)
			}
		}
	}
}
