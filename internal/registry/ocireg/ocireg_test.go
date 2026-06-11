package ocireg

import (
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/bnkbom"
)

func TestTargetRefs_WithNamespace(t *testing.T) {
	tgt := &Target{Host: "de.icr.io", Namespace: "myns"}
	img := bnkbom.Artifact{Name: "images/tmm-img", Tag: "v1", Digest: "sha256:abc"}
	chart := bnkbom.Artifact{Name: "charts/f5-tmm", Tag: "v1"}

	cases := map[string]string{
		"PushRef":         tgt.PushRef(img),
		"PushHost":        tgt.PushHost(),
		"ImagePullRef":    tgt.ImagePullRef(img),
		"ChartPullRef":    tgt.ChartPullRef(chart),
		"ImageHostPath":   tgt.ImageHostPath(),
		"ChartHostPath":   tgt.ChartHostPath(),
		"MirrorNamespace": tgt.MirrorNamespace(),
	}
	want := map[string]string{
		"PushRef":         "de.icr.io/myns/images/tmm-img:v1",
		"PushHost":        "de.icr.io",
		"ImagePullRef":    "de.icr.io/myns/images/tmm-img@sha256:abc",
		"ChartPullRef":    "oci://de.icr.io/myns/charts/f5-tmm",
		"ImageHostPath":   "de.icr.io/myns",
		"ChartHostPath":   "de.icr.io/myns",
		"MirrorNamespace": "myns",
	}
	for k, got := range cases {
		if got != want[k] {
			t.Errorf("%s = %q, want %q", k, got, want[k])
		}
	}

	// No digest → fall back to the tag.
	notag := bnkbom.Artifact{Name: "images/x", Tag: "t"}
	if got := tgt.ImagePullRef(notag); got != "de.icr.io/myns/images/x:t" {
		t.Errorf("ImagePullRef(no digest) = %q", got)
	}
}

func TestTargetRefs_NoNamespace(t *testing.T) {
	tgt := &Target{Host: "registry.local:5000"} // namespace empty
	a := bnkbom.Artifact{Name: "images/x", Tag: "t"}
	if got := tgt.PushRef(a); got != "registry.local:5000/images/x:t" {
		t.Errorf("PushRef = %q", got)
	}
	if got := tgt.ImageHostPath(); got != "registry.local:5000" {
		t.Errorf("ImageHostPath = %q", got)
	}
}
