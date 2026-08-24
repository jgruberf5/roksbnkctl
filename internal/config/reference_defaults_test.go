package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// placeholderTFDefaults are terraform defaults that are NOT the value an
// operator effectively gets, so publishing them as "the default" would be
// worse than publishing nothing. Both are CIS placeholders: Container Ingress
// Services is opt-in, and a reader told the default BIG-IP is 192.168.1.245
// would reasonably conclude one is configured.
//
// Keep this list SHORT. Every entry hides a real default from the book.
var placeholderTFDefaults = map[string]string{
	"bigip_url":      "CIS is opt-in; the terraform value is a placeholder host, not a default anyone gets",
	"bigip_username": "same placeholder credential block as bigip_url",
}

// The configuration reference publishes a `default` column. It is filled from
// the `default:"..."` struct tag, which is documentation only — nothing reads it
// at runtime. So a field whose real default lives in terraform shows an em-dash,
// and the book tells the reader nothing about what happens if they set nothing.
//
// Eighteen keys were in that state, including gateway.class_name,
// gateway.vxlan_port, bnk.far_repo_url and the testing-jumphost sizing. The
// renderer omits each of those tfvars when the field is unset, so terraform's
// default IS the effective default — it was simply never written down.
//
// This fails when a config key with no published default maps to a terraform
// variable that has a meaningful one.
func TestPublishedDefaultsCoverTheTerraformDefaults(t *testing.T) {
	root := repoRootForDemoTest(t)

	tfSrc, err := os.ReadFile(filepath.Join(root, "terraform/variables.tf"))
	if err != nil {
		t.Fatalf("read variables.tf: %v", err)
	}
	varBlock := regexp.MustCompile(`(?s)variable\s+"([^"]+)"\s*\{(.*?)\n\}`)
	defLine := regexp.MustCompile(`(?m)^\s*default\s*=\s*(.+)$`)
	tfDefaults := map[string]string{}
	for _, m := range varBlock.FindAllStringSubmatch(string(tfSrc), -1) {
		if d := defLine.FindStringSubmatch(m[2]); d != nil {
			tfDefaults[m[1]] = strings.TrimSpace(d[1])
		}
	}
	if len(tfDefaults) < 50 {
		t.Fatalf("parsed only %d terraform defaults; the regex has drifted and this "+
			"test would pass by finding nothing", len(tfDefaults))
	}

	chapter, err := os.ReadFile(filepath.Join(root, "book/src/28-configuration-reference.md"))
	if err != nil {
		t.Fatalf("read chapter 28: %v", err)
	}
	lines := strings.Split(string(chapter), "\n")

	keyCol, defCol := -1, -1
	for _, l := range lines {
		if !strings.HasPrefix(l, "| key |") {
			continue
		}
		for i, h := range strings.Split(l, "|") {
			switch strings.TrimSpace(h) {
			case "key":
				keyCol = i
			case "default":
				defCol = i
			}
		}
		break
	}
	if keyCol < 0 || defCol < 0 {
		t.Fatal("no `| key |` header with key+default columns; this test cannot locate " +
			"the columns it grades and would otherwise pass vacuously")
	}

	// "unset" is what an empty terraform default means too — those carry no
	// information worth publishing.
	trivial := map[string]bool{`""`: true, "null": true, "[]": true, "{}": true, "false": true, "0": true}

	rows := 0
	for _, l := range lines {
		if !strings.HasPrefix(l, "| `") {
			continue
		}
		cols := strings.Split(l, "|")
		if len(cols) <= defCol {
			continue
		}
		rows++
		if d := strings.TrimSpace(cols[defCol]); d != "—" && d != "-" && d != "" {
			continue // already published
		}
		key := strings.Trim(strings.TrimSpace(cols[keyCol]), "`")

		// The tfvar is named for the config key, sometimes with the module prefix.
		for _, cand := range []string{key, "cneinstance_" + key, "gateway_" + key, "roks_" + key, "cluster_" + key, "f5_" + key} {
			v, ok := tfDefaults[cand]
			if !ok || trivial[v] {
				continue
			}
			if _, exempt := placeholderTFDefaults[key]; exempt {
				break
			}
			t.Errorf("config key %q publishes no default, but terraform %s defaults to %s.\n"+
				"An operator reading the reference cannot tell what they get by setting nothing. "+
				"Add a `default:\"...\"` struct tag in internal/config/workspace.go (it is documentation "+
				"only — nothing reads it at runtime), or record it in placeholderTFDefaults with the "+
				"reason the terraform value is not a real default.", key, cand, v)
			break
		}
	}
	if rows < 100 {
		t.Errorf("only %d reference rows parsed; expected 100+", rows)
	}

	// An exemption must not go stale. If a placeholder key later publishes a
	// real default, the entry is hiding nothing and should go — otherwise the
	// list only ever grows and quietly becomes the place defaults go to die.
	published := map[string]bool{}
	for _, l := range lines {
		if !strings.HasPrefix(l, "| `") {
			continue
		}
		cols := strings.Split(l, "|")
		if len(cols) <= defCol {
			continue
		}
		if d := strings.TrimSpace(cols[defCol]); d != "—" && d != "-" && d != "" {
			published[strings.Trim(strings.TrimSpace(cols[keyCol]), "`")] = true
		}
	}
	for key, why := range placeholderTFDefaults {
		if published[key] {
			t.Errorf("%q now publishes a default, so remove it from placeholderTFDefaults (was: %s)", key, why)
		}
	}
}
