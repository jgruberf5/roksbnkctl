package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()

	s1, err := Load(dir)
	if err != nil {
		t.Fatalf("Load on empty dir: %v", err)
	}
	s1.Set("VPC_ID", "vpc-0abc123")
	s1.Set("IGW_ID", "igw-0xyz789")
	if err := s1.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	s2, err := Load(dir)
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	if s2.Get("VPC_ID") != "vpc-0abc123" {
		t.Errorf("VPC_ID: got %q, want %q", s2.Get("VPC_ID"), "vpc-0abc123")
	}
	if s2.Get("IGW_ID") != "igw-0xyz789" {
		t.Errorf("IGW_ID: got %q, want %q", s2.Get("IGW_ID"), "igw-0xyz789")
	}
}

func TestLoad_MissingFileReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	// No state.env written yet.
	s, err := Load(dir)
	if err != nil {
		t.Fatalf("expected nil error for missing file, got: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil State")
	}
	if got := s.Get("ANYTHING"); got != "" {
		t.Errorf("expected empty value, got %q", got)
	}
}

func TestLoad_CorruptFileReturnsError(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.env")
	if err := os.WriteFile(p, []byte("not-kv-line\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for corrupt file, got nil")
	}
}

func TestLoad_IgnoresComments(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.env")
	content := "# this is a comment\nVPC_ID=vpc-001\n# another comment\nIGW_ID=igw-002\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Get("VPC_ID") != "vpc-001" {
		t.Errorf("VPC_ID: got %q", s.Get("VPC_ID"))
	}
	if s.Get("IGW_ID") != "igw-002" {
		t.Errorf("IGW_ID: got %q", s.Get("IGW_ID"))
	}
}

func TestSave_CreatesDir(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "nested", "dir")

	s, _ := Load(dir) // dir doesn't exist yet, Load returns empty State
	s.Set("KEY", "val")
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	s2, err := Load(dir)
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	if s2.Get("KEY") != "val" {
		t.Errorf("KEY: got %q, want val", s2.Get("KEY"))
	}
}

func TestGet_MissingKeyReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	s, _ := Load(dir)
	if got := s.Get("NO_SUCH_KEY"); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

// TestMarkReadOnly_SaveIsNoOp is the regression guard for the up --dry-run
// state-pollution bug: a dry-run sets placeholder IDs in memory, but Save must
// never write them to the real state.env on disk.
func TestMarkReadOnly_SaveIsNoOp(t *testing.T) {
	dir := t.TempDir()

	// Pre-existing real state on disk (as if a real `up` had run).
	real, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	real.Set("BNK_INT_SUBNET", "subnet-0realid")
	if err := real.Save(); err != nil {
		t.Fatalf("Save real state: %v", err)
	}

	// Now simulate a dry-run: load, mark read-only, overlay a placeholder, save.
	dry, err := Load(dir)
	if err != nil {
		t.Fatalf("Load for dry-run: %v", err)
	}
	dry.MarkReadOnly()
	if !dry.ReadOnly() {
		t.Fatal("ReadOnly() = false after MarkReadOnly()")
	}
	dry.Set("BNK_INT_SUBNET", "dry-run-subnet-bnk-int") // the polluting placeholder
	if dry.Get("BNK_INT_SUBNET") != "dry-run-subnet-bnk-int" {
		t.Error("Set should still update the in-memory map in read-only mode")
	}
	if err := dry.Save(); err != nil {
		t.Fatalf("Save in read-only mode should be a no-op, got error: %v", err)
	}

	// Reload from disk — the real ID must survive, the placeholder must NOT.
	after, err := Load(dir)
	if err != nil {
		t.Fatalf("Load after dry-run save: %v", err)
	}
	if got := after.Get("BNK_INT_SUBNET"); got != "subnet-0realid" {
		t.Errorf("dry-run polluted disk: BNK_INT_SUBNET = %q, want subnet-0realid", got)
	}
}

// TestMarkReadOnly_NoFileCreatedOnFreshDir verifies a dry-run against a dir
// with no prior state.env leaves the disk untouched (no file created).
func TestMarkReadOnly_NoFileCreatedOnFreshDir(t *testing.T) {
	dir := t.TempDir()
	s, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	s.MarkReadOnly()
	s.Set("VPC_ID", "vpc-dry-run")
	if err := s.Save(); err != nil {
		t.Fatalf("Save no-op: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "state.env")); !os.IsNotExist(err) {
		t.Errorf("read-only Save created state.env on disk (stat err = %v); expected none", err)
	}
}
