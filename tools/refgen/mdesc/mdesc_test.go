package mdesc

import "testing"

func TestPrepare(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{{
		name: "the #239 case: a bare placeholder became a live tag",
		in:   "Workspace is ~/.roksbnkctl/<name>/config.yaml.",
		want: "Workspace is ~/.roksbnkctl/`<name>`/config.yaml.",
	}, {
		name: "two placeholders in one value",
		in:   `concrete "<ip>[:<port>]" strings`,
		want: "concrete \"`<ip>`[:`<port>`]\" strings",
	}, {
		// The gap that let this reach the published book: tfvars-md's local
		// version matched an identifier only, so a placeholder with a space
		// stayed bare and mdbook read `<cluster` as a tag with a `vpc` attribute.
		name: "placeholder containing a space",
		in:   "scoped to the cluster's OWN VPC (serviceName=is, vpcId=<cluster vpc>).",
		want: "scoped to the cluster's OWN VPC (serviceName=is, vpcId=`<cluster vpc>`).",
	}, {
		name: "already inside backticks — must not touch it",
		in:   "the CNEInstance's advanced.`<component>`.env[] lists",
		want: "the CNEInstance's advanced.`<component>`.env[] lists",
	}, {
		name: "wraps outside a span, leaves the inside alone",
		in:   "pass `--server <name>` to reach <host>",
		want: "pass `--server <name>` to reach `<host>`",
	}, {
		name: "double-backtick span containing a backtick",
		in:   "``a ` b <c>`` and <d>",
		want: "``a ` b <c>`` and `<d>`",
	}, {
		name: "unclosed backtick is not a span, so wrapping continues",
		in:   "half `open <name> and more",
		want: "half `open `<name>` and more",
	}, {
		// Not a placeholder, so it is escaped rather than wrapped — otherwise it
		// would still open a tag.
		name: "a bare comparison is escaped, not wrapped",
		in:   "fails when x <y and z> w",
		want: "fails when x &lt;y and z&gt; w",
	}, {
		// The boundary: two words is a placeholder, three is prose.
		name: "two-word placeholder is wrapped",
		in:   "vpcId=<cluster vpc> here",
		want: "vpcId=`<cluster vpc>` here",
	}, {
		name: "a lone bracket with no letter after it",
		in:   "range is < 5 and > 1",
		want: "range is &lt; 5 and &gt; 1",
	}, {
		name: "unterminated bracket is escaped",
		in:   "starts <name but never closes",
		want: "starts &lt;name but never closes",
	}, {
		name: "text with no angle brackets is unchanged",
		in:   "Backend is the execution-backend spec.",
		want: "Backend is the execution-backend spec.",
	}, {
		name: "empty",
		in:   "",
		want: "",
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Prepare(c.in); got != c.want {
				t.Errorf("Prepare(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
		})
	}
}

// The chapters are regenerated in place on every build, and the generators are
// run over their own prior output during development. A non-idempotent Prepare
// would add a layer of backticks or entities on each pass until the text was
// unreadable.
func TestPrepareIsIdempotent(t *testing.T) {
	for _, in := range []string{
		"~/.roksbnkctl/<name>/config.yaml",
		`"<ip>[:<port>]"`,
		"advanced.`<component>`.env[]",
		"vpcId=<cluster vpc>",
		"fails when x <y",
		"range is < 5",
	} {
		once := Prepare(in)
		if twice := Prepare(once); twice != once {
			t.Errorf("not idempotent for %q:\n once %q\ntwice %q", in, once, twice)
		}
	}
}

func TestCell(t *testing.T) {
	cases := []struct{ in, want string }{
		{`"local" | "docker" | "ssh:<target>"`, "\"local\" \\| \"docker\" \\| \"ssh:`<target>`\""},
		{"two\nlines", "two lines"},
		{"plain", "plain"},
	}
	for _, c := range cases {
		if got := Cell(c.in); got != c.want {
			t.Errorf("Cell(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}
