package tf

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// #189. TMM's replicas are pinned to separate nodes across separate zones by the
// placement F5's own reference capture prescribes, while their persistent volume
// is shared. On the stock ROKS default class (ibmc-vpc-block-*, ReadWriteOnce,
// zonal) exactly one replica can bind it and the rest stay Pending — which is
// what a correctly reconciling TMM runs into the moment containerPlatform stops
// lying about the platform.
//
// So the StorageClass has to be selectable, and the value has to REACH the
// CNEInstance. Asserted through both render bodies: a setting that reaches the
// full path but not the sparse one is a trap this renderer has a history of.
func TestStorageClassNameReachesTerraform(t *testing.T) {
	for name, render := range map[string]func(io.Writer, *config.Workspace, *config.RegistryMirror) error{
		"full":   renderFullBody,
		"sparse": renderSparseBody,
	} {
		t.Run(name, func(t *testing.T) {
			ws := fullyPopulatedWorkspace(t)
			if name == "sparse" {
				ws.Prefix = ""
			}
			ws.BNK.StorageClassName = "ibmc-vpc-file-regional"

			var buf bytes.Buffer
			if err := render(&buf, ws, nil); err != nil {
				t.Fatal(err)
			}
			want := `cneinstance_storage_class_name = "ibmc-vpc-file-regional"`
			if !strings.Contains(buf.String(), want) {
				t.Errorf("%s body: missing %s", name, want)
			}

			// Unset must render nothing, so the CR keeps its own default rather
			// than being pinned to an empty string.
			ws2 := fullyPopulatedWorkspace(t)
			if name == "sparse" {
				ws2.Prefix = ""
			}
			ws2.BNK.StorageClassName = ""
			var buf2 bytes.Buffer
			if err := render(&buf2, ws2, nil); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(buf2.String(), "cneinstance_storage_class_name") {
				t.Errorf("%s body: unset StorageClassName still rendered the tfvar", name)
			}
		})
	}
}

// TestEveryRootVariableIsRead passes as soon as ANY module references
// var.<name> — including a wrapper that receives it. So a root variable that is
// declared and rendered, but never PASSED DOWN from terraform/main.tf, satisfies
// that guard while doing nothing. It happened twice while writing this change.
//
// This asserts the two hops the root-level guard cannot see.
func TestStorageClassNameIsPassedDownTheModuleChain(t *testing.T) {
	read := func(rel ...string) string {
		b, err := os.ReadFile(filepath.Join(append([]string{"..", "..", "terraform"}, rel...)...))
		if err != nil {
			t.Fatalf("read %v: %v", rel, err)
		}
		return string(b)
	}
	// Whitespace-tolerant: terraform fmt aligns the `=` across a block, so an
	// exact-spacing assertion would fail on formatting rather than on substance.
	for _, tc := range []struct{ file, want, why string }{
		{"main.tf", `cneinstance_storage_class_name\s*=\s*var\.cneinstance_storage_class_name`,
			"the root never passes it to module.cne_instance, so the setting is inert"},
		{filepath.Join("modules", "cne_instance", "main.tf"), `cneinstance_storage_class_name\s*=\s*var\.cneinstance_storage_class_name`,
			"the wrapper never passes it to the leaf module"},
		{filepath.Join("modules", "cne_instance", "modules", "cneinstance", "main.tf"), `storageClassName\s*=\s*var\.cneinstance_storage_class_name`,
			"the leaf never puts it on the CNEInstance spec"},
	} {
		src := read(strings.Split(tc.file, string(filepath.Separator))...)
		if !regexp.MustCompile(tc.want).MatchString(src) {
			t.Errorf("%s: %s\n  expected to match: %s", tc.file, tc.why, tc.want)
		}
	}
}
