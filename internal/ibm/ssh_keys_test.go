package ibm

import "testing"

func TestKeyTypeFromPublic(t *testing.T) {
	cases := map[string]string{
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5...": "ed25519",
		"ssh-rsa AAAAB3NzaC1yc2E...":          "rsa",
		"  ssh-rsa AAAA...":                   "rsa", // leading space tolerated
		"unknown blob":                        "ed25519",
	}
	for pub, want := range cases {
		if got := keyTypeFromPublic(pub); got != want {
			t.Errorf("keyTypeFromPublic(%q) = %q, want %q", pub, got, want)
		}
	}
}
