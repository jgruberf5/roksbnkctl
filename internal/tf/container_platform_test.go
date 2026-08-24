package tf

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// containerPlatform is a hardcoded requirement for every roksbnkctl BNK
// deployment: IBM, with no way for an operator to select anything else.
//
// It matters because the wrong value fails SILENTLY. Under "OCP" the lifecycle
// operator creates its 16 component CRs and skips CSRC entirely — no CSRC CR,
// no f5-spk-csrc pods, no macvlan-internal NAD — and logs nothing about the
// omission. Every other signal reports healthy. Under "Generic" the controller
// aborts looking for a kubeadm-config ConfigMap that no ROKS cluster has.
//
// So this guards three separate things, because "the value is IBM today" is
// not the requirement:
//
//  1. the assignment is the literal "IBM"
//  2. there is exactly one assignment
//  3. it is not reachable from a variable, local, or tfvars field
func TestContainerPlatformIsHardcodedIBM(t *testing.T) {
	src := readFloModule(t)

	assign := regexp.MustCompile(`(?m)^\s*containerPlatform\s*=\s*(.+?)\s*$`)
	all := assign.FindAllStringSubmatch(stripHCLComments(src), -1)

	if len(all) == 0 {
		t.Fatal("no containerPlatform assignment in the flo module — " +
			"it is a hardcoded requirement and must be set explicitly")
	}
	if len(all) > 1 {
		t.Fatalf("found %d containerPlatform assignments; want exactly 1 — "+
			"a second one makes which value wins depend on map ordering", len(all))
	}

	got := all[0][1]
	if got != `"IBM"` {
		t.Errorf("containerPlatform = %s; want \"IBM\".\n"+
			"OCP makes FLO skip CSRC silently; Generic makes the controller abort "+
			"on the missing kubeadm-config ConfigMap. Neither reports an error.", got)
	}
}

// TestContainerPlatformIsNotConfigurable keeps the setting from quietly becoming
// an option. The value being right is only half of it — the moment it can be
// overridden from config.yaml or the environment, an operator can reintroduce the
// silent-CSRC-skip on their own cluster, and nothing will tell them.
func TestContainerPlatformIsNotConfigurable(t *testing.T) {
	src := readFloModule(t)
	body := stripHCLComments(src)

	line := regexp.MustCompile(`(?m)^\s*containerPlatform\s*=\s*.+$`).FindString(body)
	for _, indirection := range []string{"var.", "local.", "each.", "coalesce(", "try(", "lookup("} {
		if strings.Contains(line, indirection) {
			t.Errorf("containerPlatform is assigned via %q (%s) — it must be a bare literal, "+
				"so no config.yaml field or ROKSBNKCTL_* variable can reach it",
				indirection, strings.TrimSpace(line))
		}
	}

	// No terraform variable may exist for it either: a declared-but-unread
	// variable is exactly how a setting gets wired up later by mistake.
	for _, dir := range []string{"terraform", filepath.Join("terraform", "modules", "flo", "modules", "flo")} {
		vars, err := os.ReadFile(filepath.Join("..", "..", dir, "variables.tf"))
		if err != nil {
			continue
		}
		if regexp.MustCompile(`(?m)^\s*variable\s+"[a-z_]*container_platform[a-z_]*"`).Match(vars) {
			t.Errorf("%s/variables.tf declares a container_platform variable — "+
				"the value is a hardcoded requirement, not an operator choice", dir)
		}
	}
}

func readFloModule(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "terraform", "modules", "flo", "modules", "flo", "main.tf"))
	if err != nil {
		t.Fatalf("read flo main.tf: %v", err)
	}
	return string(b)
}

// stripHCLComments removes # and // line comments so an assignment described in
// prose is never mistaken for a live one. The comment above containerPlatform
// names OCP and Generic precisely so future readers know why they are wrong;
// a scan that cannot tell code from commentary would trip over its own docs.
func stripHCLComments(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
			b.WriteString("\n")
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}
