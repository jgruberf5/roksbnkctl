package cli

import (
	"testing"
)

// TestResolveBnkResyncOpts covers the flag-validation logic in
// resolveBnkResyncOpts without touching the kube API.

func TestResolveBnkResyncOpts_SingleRoute(t *testing.T) {
	// Reset flags to defaults before each sub-test.
	setup := func() {
		flagBnkResyncNamespace = ""
		flagBnkResyncAllInNS = false
		flagBnkResyncGatewayClass = ""
		flagBnkResyncDryRun = false
	}

	t.Run("name without namespace returns error", func(t *testing.T) {
		setup()
		_, err := resolveBnkResyncOpts([]string{"nginx-route"})
		if err == nil {
			t.Fatal("expected error for missing -n, got nil")
		}
	})

	t.Run("name with namespace returns correct opts", func(t *testing.T) {
		setup()
		flagBnkResyncNamespace = "f5-cne-system"
		opts, err := resolveBnkResyncOpts([]string{"nginx-route"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if opts.Name != "nginx-route" {
			t.Errorf("expected Name=nginx-route, got %s", opts.Name)
		}
		if opts.Namespace != "f5-cne-system" {
			t.Errorf("expected Namespace=f5-cne-system, got %s", opts.Namespace)
		}
	})

	t.Run("all-in-ns without namespace returns error", func(t *testing.T) {
		setup()
		flagBnkResyncAllInNS = true
		_, err := resolveBnkResyncOpts(nil)
		if err == nil {
			t.Fatal("expected error for missing -n, got nil")
		}
	})

	t.Run("all-in-ns with namespace returns correct opts", func(t *testing.T) {
		setup()
		flagBnkResyncAllInNS = true
		flagBnkResyncNamespace = "f5-cne-system"
		opts, err := resolveBnkResyncOpts(nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !opts.AllInNamespace {
			t.Error("expected AllInNamespace=true")
		}
	})

	t.Run("gateway-class returns correct opts", func(t *testing.T) {
		setup()
		flagBnkResyncGatewayClass = "f5-bnk"
		opts, err := resolveBnkResyncOpts(nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if opts.GatewayClass != "f5-bnk" {
			t.Errorf("expected GatewayClass=f5-bnk, got %s", opts.GatewayClass)
		}
	})

	t.Run("no selector returns error", func(t *testing.T) {
		setup()
		_, err := resolveBnkResyncOpts(nil)
		if err == nil {
			t.Fatal("expected error for no selector, got nil")
		}
	})

	t.Run("multiple selectors returns error", func(t *testing.T) {
		setup()
		flagBnkResyncAllInNS = true
		flagBnkResyncNamespace = "f5-cne-system"
		flagBnkResyncGatewayClass = "f5-bnk"
		_, err := resolveBnkResyncOpts(nil)
		if err == nil {
			t.Fatal("expected error for multiple selectors, got nil")
		}
	})

	t.Run("dry-run flag propagates", func(t *testing.T) {
		setup()
		flagBnkResyncDryRun = true
		flagBnkResyncNamespace = "f5-cne-system"
		opts, err := resolveBnkResyncOpts([]string{"nginx-route"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !opts.DryRun {
			t.Error("expected DryRun=true")
		}
	})
}

// TestBnkCmdRegistered verifies that `bnk resync` appears in the root
// command's command tree.
func TestBnkCmdRegistered(t *testing.T) {
	found := false
	for _, cmd := range rootCmd.Commands() {
		if cmd.Use == "bnk" {
			found = true
			resyncFound := false
			for _, sub := range cmd.Commands() {
				if sub.Use == "resync [httproute-name]" {
					resyncFound = true
				}
			}
			if !resyncFound {
				t.Error("bnk command found but resync subcommand not registered")
			}
		}
	}
	if !found {
		t.Error("bnk command not registered on rootCmd")
	}
}
