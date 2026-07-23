package cli

import (
	"context"
	"io"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestParsePatchType(t *testing.T) {
	cases := map[string]types.PatchType{
		"strategic": types.StrategicMergePatchType,
		"merge":     types.MergePatchType,
		"json":      types.JSONPatchType,
		"apply":     types.ApplyPatchType,
	}
	for in, want := range cases {
		got, err := parsePatchType(in)
		if err != nil || got != want {
			t.Errorf("parsePatchType(%q) = %v,%v want %v", in, got, err, want)
		}
	}
	if _, err := parsePatchType("bogus"); err == nil {
		t.Error("parsePatchType(bogus) should error")
	}
}

func TestReadPatchBody(t *testing.T) {
	flagPatchStdin, flagPatchFile, flagPatchInline = true, "", ""
	b, err := readPatchBody(strings.NewReader("from-stdin"))
	if err != nil || string(b) != "from-stdin" {
		t.Errorf("stdin body = %q,%v", b, err)
	}
	flagPatchStdin, flagPatchInline = false, `{"x":1}`
	b, err = readPatchBody(nil)
	if err != nil || string(b) != `{"x":1}` {
		t.Errorf("inline body = %q,%v", b, err)
	}
	flagPatchInline = ""
	if _, err := readPatchBody(nil); err == nil {
		t.Error("no body source should error")
	}
}

func TestRunTFXPatch_MergeAnnotation(t *testing.T) {
	dc := fakeDynFor(cneObject("True", ""))
	ri := dc.Resource(cneGVR).Namespace("f5-bnk")
	patch := []byte(`{"metadata":{"annotations":{"roksbnkctl.f5.com/restartedAt":"now"}}}`)
	if err := runTFXPatch(context.Background(), ri, "bnk", types.MergePatchType, patch, "", false, "", io.Discard); err != nil {
		t.Fatalf("merge patch failed: %v", err)
	}
	got, err := ri.Get(context.Background(), "bnk", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ann := got.GetAnnotations()
	if ann["roksbnkctl.f5.com/restartedAt"] != "now" {
		t.Errorf("annotation not applied: %v", ann)
	}
}

func TestRunTFXPatch_MissingErrors(t *testing.T) {
	dc := fakeDynFor() // no object
	ri := dc.Resource(cneGVR).Namespace("f5-bnk")
	err := runTFXPatch(context.Background(), ri, "bnk", types.MergePatchType, []byte(`{}`), "", false, "", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "patching bnk") {
		t.Fatalf("patching a missing object should error, got %v", err)
	}
}

func TestReadPatchBody_B64(t *testing.T) {
	flagPatchStdin, flagPatchFile, flagPatchInline = false, "", ""
	flagPatchB64 = "eyJ4IjoxfQ==" // {"x":1}
	b, err := readPatchBody(nil)
	if err != nil || string(b) != `{"x":1}` {
		t.Fatalf("b64 body = %q,%v want {\"x\":1}", b, err)
	}
	flagPatchB64 = "not-valid-base64!!"
	if _, err := readPatchBody(nil); err == nil {
		t.Error("invalid base64 should error")
	}
	flagPatchB64 = ""
}
