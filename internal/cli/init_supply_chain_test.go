package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandHomePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	cases := map[string]string{
		"~":            home,
		"~/":           home,
		"~/a/b.tgz":    filepath.Join(home, "a/b.tgz"),
		"/abs/path":    "/abs/path",
		"relative.jwt": "relative.jwt",
		"~notme/x":     "~notme/x", // only ~/ (or bare ~) expands, not ~user
	}
	for in, want := range cases {
		if got := expandHomePath(in); got != want {
			t.Errorf("expandHomePath(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestUniqueBucketName covers the account-scoped, deterministic bucket naming that
// lets a second workspace (same account) discover + reuse a provisioned bucket.
func TestUniqueBucketName(t *testing.T) {
	const acct = "0b5a00334eaf9eb9339d2ab48f394755"
	got := uniqueBucketName("bnk-artifacts", acct)
	want := "bnk-artifacts-0b5a00334eaf" // base + first 12 of the account id
	if got != want {
		t.Fatalf("uniqueBucketName = %q, want %q", got, want)
	}
	// Deterministic: same account -> same name (so a new workspace rediscovers it).
	if again := uniqueBucketName("bnk-artifacts", acct); again != got {
		t.Errorf("not deterministic: %q vs %q", again, got)
	}
	// Different accounts -> different (globally-unique) names.
	if other := uniqueBucketName("bnk-artifacts", "9999abcd0000ffff"); other == got {
		t.Errorf("expected different name for a different account, got %q", other)
	}
	// Non-alphanumerics in the account id are stripped; empty id -> base unchanged.
	if uniqueBucketName("b", "") != "b" {
		t.Errorf("empty account id should return base unchanged")
	}
	if uniqueBucketName("b", "AB-cd_12") != "b-abcd12" {
		t.Errorf("expected sanitized lowercase token, got %q", uniqueBucketName("b", "AB-cd_12"))
	}
}
