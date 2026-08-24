package config

import (
	"strings"
	"testing"
)

// OverrideFromEnv's tables carried ROKSBNKCTL_HUGEPAGES_SIZE and
// ROKSBNKCTL_HUGEPAGES_COUNT twice each, as byte-identical rows. The duplicate
// set the same field to the same value, so no config was ever wrong — but each
// table row also appends to the "applied" list that `init --override-from-env`
// prints, so the operator was told twice that one variable had been applied.
//
// The existing surface guard could not see it: it compares the code's names
// against SupportedOverrideNames with slices.Contains, and set membership is
// blind to a repeat. This asserts on the report instead, which is the thing
// the operator actually reads.
func TestEachOverrideIsReportedOnce(t *testing.T) {
	// Every supported override, set at once, so a duplicate anywhere in any of
	// the tables shows up — not just the two known ones.
	for _, name := range SupportedOverrideNames() {
		t.Setenv(name, envSampleValue(name))
	}

	var ws Workspace
	applied := OverrideFromEnv(&ws)

	seen := map[string]int{}
	for _, line := range applied {
		// Each entry is "field (ROKSBNKCTL_NAME)"; key on the variable name.
		open := strings.LastIndex(line, "(")
		if open < 0 || !strings.HasSuffix(line, ")") {
			t.Errorf("applied entry %q is not in the expected \"field (ENV_NAME)\" form", line)
			continue
		}
		seen[line[open+1:len(line)-1]]++
	}

	for name, n := range seen {
		if n > 1 {
			t.Errorf("%s reported %d times in the applied list; want 1 — "+
				"a duplicate row in one of OverrideFromEnv's tables", name, n)
		}
	}
}

// envSampleValue returns a value that parses for the variable's type, so the
// row actually fires. A value that fails strconv.Atoi is skipped by the int
// table, which would hide a duplicate rather than expose it.
func envSampleValue(name string) string {
	switch {
	case strings.HasSuffix(name, "_COUNT"),
		strings.HasSuffix(name, "_REPLICAS"),
		strings.HasSuffix(name, "_MAX_SKEW"),
		strings.HasSuffix(name, "_PREFIXLEN"),
		strings.HasSuffix(name, "_PER_ZONE"):
		return "1"
	}
	return "x"
}
