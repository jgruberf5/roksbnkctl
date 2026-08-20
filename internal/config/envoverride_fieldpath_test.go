package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The report string tells the operator WHICH config field an env var replaced.
// A wrong path is a lie that survives every other test: the value lands
// correctly and the report names something else.
func TestEveryTableRowsFieldPathIsRealYAML(t *testing.T) {
	for _, o := range stringOverrides {
		ws := &Workspace{}
		o.set(ws, "sentinel-value")
		b, err := yaml.Marshal(ws)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := yaml.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		cur := any(got)
		ok := true
		for _, seg := range strings.Split(o.field, ".") {
			m, isMap := cur.(map[string]any)
			if !isMap {
				ok = false
				break
			}
			cur, ok = m[seg]
			if !ok {
				break
			}
		}
		if !ok {
			t.Errorf("%s reports field %q, but that path does not exist in the marshalled workspace:\n%s",
				o.env, o.field, b)
			continue
		}
		if s, isStr := cur.(string); !isStr || s != "sentinel-value" {
			t.Errorf("%s reports field %q, but the value landed elsewhere (that path holds %v)",
				o.env, o.field, cur)
		}
	}
}
