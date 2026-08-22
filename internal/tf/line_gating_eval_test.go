package tf

import (
	"encoding/json"
	"strconv"
	"testing"
)

// The line-gating guards elsewhere assert on terraform SOURCE TEXT. An
// adversarial review showed three valid, type-correct mutations that left all
// of them green:
//
//   - flipping `? 1 : 0` to `? 0 : 1` on the CIS resources, which moves the
//     whole 2.3-only CIS install onto 2.4 and off 2.3. The regex matched
//     `count = ... local.line_pre_24` and said nothing about direction.
//   - replacing `line_pre_24 = var.bnk_line != "2.4"` with a COMMENT of that
//     line plus `= var.bnk_line == "2.3"` — the exact defect the test's own
//     failure message names. A comment is text, and a text scan reads text.
//   - emptying `scc_policy_assignments_24` to `[]`, which still passed because
//     the service-account string the test looked for also appears in the 2.3
//     list further up the same file.
//
// These evaluate the values instead. A comment has no value, a polarity flip
// changes one, and an emptied list has a different length.

func consoleNumber(t *testing.T, module []string, tfvars, expr string) float64 {
	t.Helper()
	encoded := consoleJSON(t, module, tfvars, expr)
	var n json.Number
	if err := json.Unmarshal([]byte(encoded), &n); err != nil {
		t.Fatalf("decode %q as a number: %v", encoded, err)
	}
	f, err := strconv.ParseFloat(n.String(), 64)
	if err != nil {
		t.Fatalf("parse %q: %v", n.String(), err)
	}
	return f
}

func consoleBool(t *testing.T, module []string, tfvars, expr string) bool {
	t.Helper()
	encoded := consoleJSON(t, module, tfvars, expr)
	var b bool
	if err := json.Unmarshal([]byte(encoded), &b); err != nil {
		t.Fatalf("decode %q as a bool: %v", encoded, err)
	}
	return b
}

var lineGatedModules = map[string][]string{
	"cneinstance": {"cne_instance", "modules", "cneinstance"},
	"flo":         {"flo", "modules", "flo"},
	"gateway":     {"gateway"},
}

// line_pre_24 must be TRUE for 2.3, FALSE for 2.4, and TRUE for anything else.
// The unknown case is the one that matters: a line nobody has taught this build
// about must keep the 2.3 behaviour rather than silently take the 2.4 path,
// because 2.3 is the behaviour that has shipped.
func TestLinePre24IsEvaluatedNotJustSpelled(t *testing.T) {
	for name, module := range lineGatedModules {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, tc := range []struct {
				line string
				want bool
			}{
				{"2.3", true},
				{"2.4", false},
				{"2.5", true},
				{"", true},
			} {
				vars := "bnk_line = " + strconv.Quote(tc.line) + "\n"
				got := consoleBool(t, module, vars, "local.line_pre_24")
				if got != tc.want {
					t.Errorf("%s: bnk_line=%q gave line_pre_24=%v, want %v", name, tc.line, got, tc.want)
				}
			}
		})
	}
}

// 2.4 collapses nineteen SCC bindings to one. Asserting on the LENGTH means an
// emptied 2.4 list fails here even though the service-account name it used to
// be checked by still appears elsewhere in the file.
func TestTheSCCSetCollapsesToExactlyOneOnTwoFour(t *testing.T) {
	module := lineGatedModules["cneinstance"]

	on23 := consoleNumber(t, module, "bnk_line = \"2.3\"\n", "length(local.scc_policy_assignments)")
	if on23 < 2 {
		t.Errorf("2.3 must keep its full SCC set, got %v", on23)
	}

	on24 := consoleNumber(t, module, "bnk_line = \"2.4\"\n", "length(local.scc_policy_assignments)")
	if on24 != 1 {
		t.Errorf("2.4 grants SCC to the operator alone, so exactly one binding; got %v.\n"+
			"An empty set would also be shorter than 2.3 and would fail at pod admission, "+
			"which is why this asserts the exact count rather than 'fewer'.", on24)
	}
}
