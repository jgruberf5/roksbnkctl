package cli

import (
	"context"
	"io"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRunTFXDelete_Existing(t *testing.T) {
	dc := fakeDynFor(cneObject("True", ""))
	ri := dc.Resource(cneGVR).Namespace("f5-bnk")
	if err := runTFXDelete(context.Background(), ri, "bnk", false, io.Discard); err != nil {
		t.Fatalf("deleting an existing object should succeed, got %v", err)
	}
	if _, err := ri.Get(context.Background(), "bnk", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("object should be gone after delete, Get err = %v", err)
	}
}

func TestRunTFXDelete_MissingIgnored(t *testing.T) {
	dc := fakeDynFor() // nothing to delete
	ri := dc.Resource(cneGVR).Namespace("f5-bnk")
	if err := runTFXDelete(context.Background(), ri, "bnk", true, io.Discard); err != nil {
		t.Fatalf("--ignore-not-found should swallow a missing object, got %v", err)
	}
}

func TestRunTFXDelete_MissingNotIgnored(t *testing.T) {
	dc := fakeDynFor()
	ri := dc.Resource(cneGVR).Namespace("f5-bnk")
	err := runTFXDelete(context.Background(), ri, "bnk", false, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "deleting bnk") {
		t.Fatalf("missing object without --ignore-not-found should error, got %v", err)
	}
}
