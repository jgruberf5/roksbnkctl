package cli

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

func runRegistryTarget(_ *cobra.Command, args []string) error {
	name, ws, err := loadRegistryWorkspace()
	if err != nil {
		return err
	}

	// No arguments (and no stdin flag) → show the current config.
	if len(args) == 0 && !flagRegistryPasswordStdin {
		printRegistryTarget(ws)
		return nil
	}

	if ws.Registry == nil {
		ws.Registry = &config.RegistryCfg{}
	}
	reg := ws.Registry

	// --password-stdin sets generic_password from stdin, keeping the token out
	// of argv + shell history.
	if flagRegistryPasswordStdin {
		field := "generic_password"
		if len(args) >= 1 {
			field = args[0]
		}
		if field != "generic_password" {
			return fmt.Errorf("--password-stdin only applies to generic_password")
		}
		raw, rerr := io.ReadAll(os.Stdin)
		if rerr != nil {
			return fmt.Errorf("reading password from stdin: %w", rerr)
		}
		raw = bytes.TrimRight(raw, "\r\n")
		if len(raw) == 0 {
			return fmt.Errorf("no password read from stdin")
		}
		reg.GenericPasswordB64 = base64.StdEncoding.EncodeToString(raw)
		return saveRegistryTarget(name, ws, "generic_password")
	}

	first := args[0]
	if registryTargetKinds[first] {
		reg.Target = first
		return saveRegistryTarget(name, ws, "target = "+first)
	}

	// Otherwise a field name, which needs a value.
	if len(args) != 2 {
		return fmt.Errorf("setting %q needs a value: roksbnkctl registry target %s <value>", first, first)
	}
	val := args[1]
	switch first {
	case "icr_host":
		reg.ICRHost = val
	case "icr_namespace":
		reg.ICRNamespace = val
	case "generic_host":
		reg.GenericHost = val
	case "generic_repo_prefix":
		reg.GenericRepoPrefix = val
	case "generic_username":
		reg.GenericUsername = val
	case "generic_ca":
		// Takes a FILE, not a literal: the CA is what you generated, so it is read
		// from disk and its fingerprint recorded alongside it. Recording both means a
		// later capture (if the PEM is ever cleared) is still authenticated by the pin.
		pemBytes, rerr := os.ReadFile(val)
		if rerr != nil {
			return fmt.Errorf("reading generic_ca %s: %w", val, rerr)
		}
		trimmed := strings.TrimSpace(string(pemBytes))
		if !strings.Contains(trimmed, "BEGIN CERTIFICATE") {
			return fmt.Errorf("%s does not look like a PEM certificate", val)
		}
		reg.GenericCAB64 = base64.StdEncoding.EncodeToString([]byte(trimmed))
		if fp, ferr := pemRootFingerprint(trimmed); ferr == nil {
			reg.GenericCASHA256 = fp
			fmt.Fprintf(os.Stderr, "  pinned SHA-256 %s\n", fp)
		}
	case "generic_ca_sha256":
		fp := normalizeCAPin(val)
		if len(fp) != 64 {
			return fmt.Errorf("generic_ca_sha256 %q is not a SHA-256 hex digest (64 hex chars)", val)
		}
		reg.GenericCASHA256 = fp
	case "generic_password":
		reg.GenericPasswordB64 = base64.StdEncoding.EncodeToString([]byte(val))
	default:
		return fmt.Errorf("unknown registry target arg %q\n  kinds:  icr|generic\n  fields: icr_host icr_namespace generic_host generic_repo_prefix generic_username generic_password generic_ca generic_ca_sha256", first)
	}
	return saveRegistryTarget(name, ws, first)
}
