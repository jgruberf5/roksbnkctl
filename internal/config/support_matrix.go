package config

import (
	_ "embed"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed support_matrix.yaml
var supportMatrixYAML []byte

// SupportLine is one row of the matrix: a BNK release and what it can drive.
type SupportLine struct {
	BNK          string   `yaml:"bnk"`
	Contract     []int    `yaml:"contract"`
	NetworkModes []string `yaml:"network_modes"`
	Notes        string   `yaml:"notes"`
}

type supportMatrix struct {
	Lines []SupportLine `yaml:"lines"`
}

// SupportedLines returns the matrix, parsed from the embedded data file.
func SupportedLines() ([]SupportLine, error) {
	var m supportMatrix
	if err := yaml.Unmarshal(supportMatrixYAML, &m); err != nil {
		return nil, fmt.Errorf("parsing the embedded support matrix: %w", err)
	}
	return m.Lines, nil
}

// LookupLine finds the row for a BNK release line, e.g. "2.4".
func LookupLine(bnk string) (SupportLine, bool) {
	lines, err := SupportedLines()
	if err != nil {
		return SupportLine{}, false
	}
	for _, l := range lines {
		if l.BNK == bnk {
			return l, true
		}
	}
	return SupportLine{}, false
}

// CheckSupported reports whether a BNK line can drive a cluster of this network
// mode and contract schema.
//
// The three failures are kept distinct on purpose. "Unknown BNK line", "this line
// does not do multi-NIC" and "this line cannot read that contract" have different
// fixes, and collapsing them into one message costs the reader the answer.
func CheckSupported(bnkLine, networkMode string, schema int) error {
	lines, err := SupportedLines()
	if err != nil {
		return err
	}
	var l SupportLine
	var ok bool
	for _, x := range lines {
		if x.BNK == bnkLine {
			l, ok = x, true
			break
		}
	}
	if !ok {
		known := make([]string, 0, len(lines))
		for _, x := range lines {
			known = append(known, x.BNK)
		}
		sort.Strings(known)
		return fmt.Errorf("BNK %s is not a supported release line (this build supports %s).\n"+
			"  The line is derived from bnk.manifest_version — check it names a published manifest",
			bnkLine, strings.Join(known, ", "))
	}
	if networkMode != "" && !contains(l.NetworkModes, networkMode) {
		return fmt.Errorf("BNK %s does not support %s clusters (it supports %s).\n"+
			"  A cluster's network mode is fixed when it is created, so this needs either a\n"+
			"  different bnk.manifest_version or a cluster built in a mode %s supports",
			bnkLine, networkMode, strings.Join(l.NetworkModes, ", "), bnkLine)
	}
	if schema > 0 && !containsInt(l.Contract, schema) {
		return fmt.Errorf("BNK %s cannot read a cluster recorded at contract schema %d (it reads %s).\n"+
			"  cluster-outputs.json was written by a different version of roksbnkctl",
			bnkLine, schema, joinInts(l.Contract))
	}
	return nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func containsInt(xs []int, n int) bool {
	for _, x := range xs {
		if x == n {
			return true
		}
	}
	return false
}

func joinInts(xs []int) string {
	parts := make([]string, 0, len(xs))
	for _, x := range xs {
		parts = append(parts, fmt.Sprint(x))
	}
	return strings.Join(parts, ", ")
}
