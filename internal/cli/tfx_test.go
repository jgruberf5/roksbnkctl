package cli

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestParseGVR(t *testing.T) {
	cases := []struct {
		in   string
		want schema.GroupVersionResource
		bad  bool
	}{
		{"apps/v1/deployments", schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, false},
		{"k8s.f5.com/v1/cneinstances", schema.GroupVersionResource{Group: "k8s.f5.com", Version: "v1", Resource: "cneinstances"}, false},
		{"v1/pods", schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}, false},
		{"/v1/configmaps", schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}, false},
		{"admissionregistration.k8s.io/v1/validatingadmissionpolicybindings",
			schema.GroupVersionResource{Group: "admissionregistration.k8s.io", Version: "v1", Resource: "validatingadmissionpolicybindings"}, false},
		{"pods", schema.GroupVersionResource{}, true},
		{"", schema.GroupVersionResource{}, true},
		{"a/b/c/d", schema.GroupVersionResource{}, true},
		{"apps//deployments", schema.GroupVersionResource{}, true},
	}
	for _, c := range cases {
		got, err := parseGVR(c.in)
		if c.bad {
			if err == nil {
				t.Errorf("parseGVR(%q) = %+v, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseGVR(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseGVR(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestParseWaitFor(t *testing.T) {
	t.Run("condition", func(t *testing.T) {
		m, err := parseWaitFor("condition=CNEControllerAvailable=True")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		cm, ok := m.(conditionMatcher)
		if !ok || cm.typ != "CNEControllerAvailable" || cm.status != "True" {
			t.Fatalf("got %#v, want conditionMatcher{CNEControllerAvailable,True}", m)
		}
	})
	t.Run("jsonpath", func(t *testing.T) {
		m, err := parseWaitFor("jsonpath=status.state=Active")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		jm, ok := m.(jsonpathMatcher)
		if !ok || jm.expr != "status.state" || jm.want != "Active" {
			t.Fatalf("got %#v, want jsonpathMatcher{status.state,Active}", m)
		}
	})
	for _, bad := range []string{"", "nope", "condition=OnlyType", "jsonpath=onlypath"} {
		if _, err := parseWaitFor(bad); err == nil {
			t.Errorf("parseWaitFor(%q) = nil error, want error", bad)
		}
	}
}
