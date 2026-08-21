package tf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// #150. `registry replicate` reports per-artifact failures and exits nonzero,
// but it writes the mirror record first, with only the artifacts that copied.
// The partial write is deliberate — it lets a re-run skip what is already there
// — but RegistryMirror had no way to say "incomplete", so from that moment
// every consumer treated a partial mirror as finished.
//
// The tfvars render is where that becomes expensive: it points every image and
// chart at the mirror, terraform applies without complaint, and the failure
// surfaces minutes later on a node as ImagePullBackOff with nothing connecting
// it back to a replicate that partially failed.
//
// #112 taught this path to check the record's IDENTITY. This checks that the
// mirror it names actually holds what the install will ask for.

func writeWS(t *testing.T) *config.Workspace {
	t.Helper()
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	if err := config.SaveWorkspace("ws", &config.Workspace{}); err != nil {
		t.Fatal(err)
	}
	return &config.Workspace{Registry: &config.RegistryCfg{
		Target:            "generic",
		GenericHost:       "artifactory.example.com",
		GenericRepoPrefix: "docker-local",
	}}
}

func mirrorRec(missing int) *config.RegistryMirror {
	return &config.RegistryMirror{
		Target:       "generic",
		Namespace:    "docker-local",
		ChartHost:    "artifactory.example.com/docker-local",
		ImageHost:    "artifactory.example.com/docker-local",
		MissingCount: missing,
	}
}

func TestWriteTFVarsRefusesAnIncompleteMirror(t *testing.T) {
	ws := writeWS(t)
	if err := config.WriteRegistryMirror("ws", mirrorRec(3)); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "terraform.tfvars")
	const existing = "# rendered earlier\n"
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	err := WriteTFVarsForWorkspace(path, "ws", ws, "", "")
	if err == nil {
		t.Fatal("a mirror missing artifacts must not become the install's image source")
	}
	// The refusal has to be actionable and has to say how many, or the operator
	// cannot tell a one-artifact flake from a mirror that barely copied.
	for _, want := range []string{"3 artifact", "registry replicate", "registry diff"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal should mention %q, got:\n%s", want, err)
		}
	}

	// Refused BEFORE os.Create, so the previous render survives — same
	// invariant #112 established.
	got, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatalf("the existing tfvars was removed: %v", rerr)
	}
	if string(got) != existing {
		t.Errorf("a refused render must not touch the existing tfvars; it now holds %q", got)
	}
}

// A complete mirror must still render — the whole air-gap path depends on it,
// and a check that refused everything would be worse than no check.
func TestWriteTFVarsRendersACompleteMirror(t *testing.T) {
	ws := writeWS(t)
	if err := config.WriteRegistryMirror("ws", mirrorRec(0)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "terraform.tfvars")
	if err := WriteTFVarsForWorkspace(path, "ws", ws, "", ""); err != nil {
		t.Fatalf("a complete mirror must render: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "artifactory.example.com/docker-local") {
		t.Error("the redirect onto the mirror is missing from the render")
	}
}

// Records written before missing_count existed have no such field, so they
// decode as 0. They must keep working: they describe mirrors whose replicate
// outcome is unknown, and treating unknown as incomplete would break workspaces
// that are fine.
func TestARecordWithoutTheFieldIsTreatedAsComplete(t *testing.T) {
	ws := writeWS(t)

	// Written as raw JSON with no missing_count key at all — the on-disk shape
	// of every record that predates this change.
	dir := filepath.Join(os.Getenv(config.ROKSBNKCTLHomeEnv), "ws")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := `{"target":"generic","namespace":"docker-local",` +
		`"chart_host":"artifactory.example.com/docker-local",` +
		`"image_host":"artifactory.example.com/docker-local"}`
	if err := os.WriteFile(filepath.Join(dir, "registry-mirror.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	rec, err := config.ReadRegistryMirror("ws")
	if err != nil {
		t.Fatalf("a record without the field must still load: %v", err)
	}
	if rec.MissingCount != 0 {
		t.Errorf("an absent missing_count must decode as 0, got %d", rec.MissingCount)
	}
	if err := config.MirrorRecordIncompleteError("ws", rec); err != nil {
		t.Errorf("a legacy record must not be refused:\n%v", err)
	}

	path := filepath.Join(t.TempDir(), "terraform.tfvars")
	if err := WriteTFVarsForWorkspace(path, "ws", ws, "", ""); err != nil {
		t.Fatalf("a legacy record must still render: %v", err)
	}
}

// The count must round-trip through the record, or the render's check reads a
// field nothing ever sets.
func TestMissingCountRoundTrips(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	if err := config.SaveWorkspace("ws", &config.Workspace{}); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteRegistryMirror("ws", mirrorRec(7)); err != nil {
		t.Fatal(err)
	}
	got, err := config.ReadRegistryMirror("ws")
	if err != nil {
		t.Fatal(err)
	}
	if got.MissingCount != 7 {
		t.Errorf("MissingCount = %d, want 7", got.MissingCount)
	}

	// And a clean re-run clears it, so a workspace recovers by re-replicating
	// rather than needing the file edited by hand.
	if err := config.WriteRegistryMirror("ws", mirrorRec(0)); err != nil {
		t.Fatal(err)
	}
	got, err = config.ReadRegistryMirror("ws")
	if err != nil {
		t.Fatal(err)
	}
	if got.MissingCount != 0 {
		t.Errorf("a clean replicate must clear the flag, got %d", got.MissingCount)
	}
}
