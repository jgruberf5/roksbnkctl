package openshift

import (
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/bnkbom"
)

func TestEndpoints(t *testing.T) {
	tgt := &Target{Namespace: "bnk-mirror", RouteHost: "default-route-openshift-image-registry.apps.x.example.com"}

	img := bnkbom.Artifact{Kind: bnkbom.KindImage, Name: "images/tmm-img", Tag: "v10.159.3-0.1.5"}
	chart := bnkbom.Artifact{Kind: bnkbom.KindChart, Name: "charts/f5-tmm", Tag: "15.430.5-0.2.157"}

	// Push (route, preserves the path + tag).
	if got, want := tgt.PushRef(img), "default-route-openshift-image-registry.apps.x.example.com/bnk-mirror/images/tmm-img:v10.159.3-0.1.5"; got != want {
		t.Errorf("PushRef(image) = %q, want %q", got, want)
	}
	if got, want := tgt.PushRef(chart), "default-route-openshift-image-registry.apps.x.example.com/bnk-mirror/charts/f5-tmm:15.430.5-0.2.157"; got != want {
		t.Errorf("PushRef(chart) = %q, want %q", got, want)
	}

	// Image pull (in-cluster service; by tag without a digest, by digest with one).
	if got, want := tgt.ImagePullRef(img), "image-registry.openshift-image-registry.svc:5000/bnk-mirror/images/tmm-img:v10.159.3-0.1.5"; got != want {
		t.Errorf("ImagePullRef(no digest) = %q, want %q", got, want)
	}
	imgD := img
	imgD.Digest = "sha256:abc"
	if got, want := tgt.ImagePullRef(imgD), "image-registry.openshift-image-registry.svc:5000/bnk-mirror/images/tmm-img@sha256:abc"; got != want {
		t.Errorf("ImagePullRef(digest) = %q, want %q", got, want)
	}

	// Chart pull (route, OCI).
	if got, want := tgt.ChartPullRef(chart), "oci://default-route-openshift-image-registry.apps.x.example.com/bnk-mirror/charts/f5-tmm"; got != want {
		t.Errorf("ChartPullRef = %q, want %q", got, want)
	}

	// The two host roots the install redirect splits far_repo_url into.
	if got, want := tgt.ImageHostPath(), "image-registry.openshift-image-registry.svc:5000/bnk-mirror"; got != want {
		t.Errorf("ImageHostPath = %q, want %q", got, want)
	}
	if got, want := tgt.ChartHostPath(), "default-route-openshift-image-registry.apps.x.example.com/bnk-mirror"; got != want {
		t.Errorf("ChartHostPath = %q, want %q", got, want)
	}

	// PushAuth carries the token as the password.
	tgt.PushToken = "tok123"
	if a := tgt.PushAuth(); a == nil {
		t.Error("PushAuth nil")
	}
}
