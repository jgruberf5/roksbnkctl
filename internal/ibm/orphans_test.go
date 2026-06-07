package ibm

import "testing"

func TestMatchesPrefix(t *testing.T) {
	cases := []struct {
		name, prefix string
		want         bool
	}{
		{"test-002", "test-002", true},       // the cluster itself (bare prefix)
		{"test-002-jh-sg", "test-002", true}, // a child resource
		{"test-002-cluster-vpc", "test-002", true},
		{"test-0020-extra", "test-002", false}, // not a "-"-delimited child
		{"other-002", "test-002", false},
		{"test-002", "", false}, // empty prefix matches nothing
	}
	for _, c := range cases {
		if got := matchesPrefix(c.name, c.prefix); got != c.want {
			t.Errorf("matchesPrefix(%q, %q) = %v, want %v", c.name, c.prefix, got, c.want)
		}
	}
}

func TestSortOrphans_DependencyOrder(t *testing.T) {
	in := []OrphanResource{
		{Kind: "vpc", Name: "v"},
		{Kind: "instance", Name: "i"},
		{Kind: "cluster", Name: "c"},
		{Kind: "subnet", Name: "s"},
		{Kind: "floating_ip", Name: "f"},
		{Kind: "trusted_profile", Name: "tp"},
		{Kind: "security_group", Name: "sg"},
		{Kind: "cos_instance", Name: "cos"},
	}
	SortOrphans(in)
	want := []string{
		"cluster", "instance", "floating_ip", "subnet",
		"security_group", "vpc", "cos_instance", "trusted_profile",
	}
	for i, k := range want {
		if in[i].Kind != k {
			got := make([]string, len(in))
			for j := range in {
				got[j] = in[j].Kind
			}
			t.Fatalf("position %d = %q, want %q (full order: %v)", i, in[i].Kind, k, got)
		}
	}
}

func TestDedupeNonEmpty(t *testing.T) {
	got := dedupeNonEmpty([]string{"us-south", "", "us-south", "ca-tor", ""})
	want := []string{"us-south", "ca-tor"}
	if len(got) != len(want) {
		t.Fatalf("dedupeNonEmpty = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("dedupeNonEmpty[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
