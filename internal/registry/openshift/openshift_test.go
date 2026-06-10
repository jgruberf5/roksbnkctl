package openshift

import (
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/bnkbom"
)

func TestEndpoints(t *testing.T) {
	// Category-as-project: the namespace is NOT in the paths — the FAR category
	// (images / charts) IS the OpenShift project.
	tgt := &Target{Namespace: "bnk-mirror", RouteHost: "default-route-openshift-image-registry.apps.x.example.com"}
	const route = "default-route-openshift-image-registry.apps.x.example.com"
	const svc = "image-registry.openshift-image-registry.svc:5000"

	img := bnkbom.Artifact{Kind: bnkbom.KindImage, Name: "images/tmm-img", Tag: "v10.159.3-0.1.5"}
	chart := bnkbom.Artifact{Kind: bnkbom.KindChart, Name: "charts/f5-tmm", Tag: "15.430.5-0.2.157"}

	if got, want := tgt.PushRef(img), route+"/images/tmm-img:v10.159.3-0.1.5"; got != want {
		t.Errorf("PushRef(image) = %q, want %q", got, want)
	}
	if got, want := tgt.PushRef(chart), route+"/charts/f5-tmm:15.430.5-0.2.157"; got != want {
		t.Errorf("PushRef(chart) = %q, want %q", got, want)
	}

	// Image pull (in-cluster service; by tag without a digest, by digest with one).
	if got, want := tgt.ImagePullRef(img), svc+"/images/tmm-img:v10.159.3-0.1.5"; got != want {
		t.Errorf("ImagePullRef(no digest) = %q, want %q", got, want)
	}
	imgD := img
	imgD.Digest = "sha256:abc"
	if got, want := tgt.ImagePullRef(imgD), svc+"/images/tmm-img@sha256:abc"; got != want {
		t.Errorf("ImagePullRef(digest) = %q, want %q", got, want)
	}

	if got, want := tgt.ChartPullRef(chart), "oci://"+route+"/charts/f5-tmm"; got != want {
		t.Errorf("ChartPullRef = %q, want %q", got, want)
	}

	// The install-redirect host roots: bare service (→ <svc>/images) and bare route
	// (→ oci://<route>/charts).
	if got, want := tgt.ImageHostPath(), svc; got != want {
		t.Errorf("ImageHostPath = %q, want %q", got, want)
	}
	if got, want := tgt.ChartHostPath(), route; got != want {
		t.Errorf("ChartHostPath = %q, want %q", got, want)
	}

	tgt.PushToken = "tok123"
	if a := tgt.PushAuth(); a == nil {
		t.Error("PushAuth nil")
	}
}
