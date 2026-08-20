package cli

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

// `tfx patch` replaces the modules' 44 `curl -X PATCH` sites — rollout-restarts
// (an annotation patch), label/annotation edits, and status writes. The patch body
// arrives on stdin (or --patch / --patch-file), never interpolated into the command
// string, so cmd.exe vs sh quoting can't diverge.

var (
	flagPatchGVR         string
	flagPatchNS          string
	flagPatchName        string
	flagPatchType        string
	flagPatchInline      string
	flagPatchFile        string
	flagPatchB64         string
	flagPatchStdin       bool
	flagPatchSubresource string
	flagPatchFieldMgr    string
	flagPatchForce       bool
)

var tfxPatchCmd = &cobra.Command{
	Use:   "patch",
	Short: "Patch a resource by GVR/name (internal)",
	Long: `Applies a patch to one resource. The patch body comes from stdin
(--patch-stdin), a file (--patch-file), or inline (--patch).

  --type strategic|merge|json|apply   (default strategic)
  --subresource status                patch a subresource (e.g. status)
  --field-manager / --force           for --type apply (server-side apply)

Rollout-restart is just a strategic/merge patch that stamps
spec.template.metadata.annotations.`,
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runTFXPatchCmd,
}

func init() {
	f := tfxPatchCmd.Flags()
	f.StringVar(&flagPatchGVR, "gvr", "", "group/version/resource (required)")
	f.StringVar(&flagPatchNS, "ns", "", "namespace (empty = cluster-scoped)")
	f.StringVar(&flagPatchName, "name", "", "resource name (required)")
	f.StringVar(&flagPatchType, "type", "strategic", "patch type: strategic|merge|json|apply")
	f.StringVar(&flagPatchInline, "patch", "", "patch body inline (or use --patch-stdin / --patch-file / --patch-b64)")
	f.BoolVar(&flagPatchStdin, "patch-stdin", false, "read the patch body from stdin")
	f.StringVar(&flagPatchFile, "patch-file", "", "read the patch body from a file")
	f.StringVar(&flagPatchB64, "patch-b64", "", "base64-encoded patch body (cmd.exe-safe for terraform local-exec: no shell metacharacters)")
	f.StringVar(&flagPatchSubresource, "subresource", "", "subresource to patch (e.g. status)")
	f.StringVar(&flagPatchFieldMgr, "field-manager", "roksbnkctl", "field manager (used by --type apply)")
	f.BoolVar(&flagPatchForce, "force", false, "force ownership on conflict (--type apply)")
	tfxCmd.AddCommand(tfxPatchCmd)
}

func runTFXPatchCmd(cmd *cobra.Command, _ []string) error {
	if flagPatchGVR == "" || flagPatchName == "" {
		return fmt.Errorf("--gvr and --name are required")
	}
	gvr, err := parseGVR(flagPatchGVR)
	if err != nil {
		return err
	}
	pt, err := parsePatchType(flagPatchType)
	if err != nil {
		return err
	}
	body, err := readPatchBody(cmd.InOrStdin())
	if err != nil {
		return err
	}
	dc, err := tfxDynamic()
	if err != nil {
		return err
	}
	ri := tfxResource(dc, gvr, flagPatchNS)
	return runTFXPatch(cmdContext(cmd), ri, flagPatchName, pt, body,
		flagPatchFieldMgr, flagPatchForce, flagPatchSubresource, os.Stderr)
}

// runTFXPatch is the testable core: patch name with the given type + body.
func runTFXPatch(ctx context.Context, ri dynamic.ResourceInterface, name string, pt types.PatchType, body []byte, fieldMgr string, force bool, subresource string, logw io.Writer) error {
	opts := metav1.PatchOptions{}
	if pt == types.ApplyPatchType {
		opts.FieldManager = fieldMgr
		if force {
			opts.Force = &force
		}
	}
	var subs []string
	if subresource != "" {
		subs = []string{subresource}
	}
	if _, err := ri.Patch(ctx, name, pt, body, opts, subs...); err != nil {
		return fmt.Errorf("patching %s: %w", name, err)
	}
	fmt.Fprintf(logw, "tfx patch: %s patched (%s)\n", name, pt)
	return nil
}

func parsePatchType(s string) (types.PatchType, error) {
	switch s {
	case "strategic":
		return types.StrategicMergePatchType, nil
	case "merge":
		return types.MergePatchType, nil
	case "json":
		return types.JSONPatchType, nil
	case "apply":
		return types.ApplyPatchType, nil
	default:
		return "", fmt.Errorf("invalid --type %q: want strategic|merge|json|apply", s)
	}
}

// readPatchBody resolves the patch bytes from exactly one of stdin/file/inline/b64.
func readPatchBody(stdin io.Reader) ([]byte, error) {
	switch {
	case flagPatchStdin:
		return io.ReadAll(stdin)
	case flagPatchFile != "":
		return os.ReadFile(flagPatchFile)
	case flagPatchB64 != "":
		b, err := base64.StdEncoding.DecodeString(flagPatchB64)
		if err != nil {
			return nil, fmt.Errorf("decoding --patch-b64: %w", err)
		}
		return b, nil
	case flagPatchInline != "":
		return []byte(flagPatchInline), nil
	default:
		return nil, fmt.Errorf("no patch body: pass --patch, --patch-stdin, --patch-file, or --patch-b64")
	}
}
