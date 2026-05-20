package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/cos"
	"github.com/jgruberf5/roksbnkctl/internal/cred"
	"github.com/jgruberf5/roksbnkctl/internal/ibm"
)

var (
	flagCOSInstance  string
	flagCOSPlan      string
	flagCOSPlanID    string
	flagCOSRegion    string
	flagCOSClass     string
	flagCOSTarget    string
	flagCOSRecursive bool
	flagCOSNoClobber bool
)

var cosCmd = &cobra.Command{
	Use:   "cos",
	Short: "Manage IBM Cloud Object Storage (instances, buckets, objects)",
	Long: `roksbnkctl cos provides full CRUD on the COS supply chain BNK depends on:
COS instances (via Resource Controller), buckets, and keyed objects (FAR pull
keys, JWT licenses, etc.). All calls go through the IBM Go SDKs — no
ibmcloud CLI dependency.`,
}

// ── instance ────────────────────────────────────────────────────────

var cosInstanceCmd = &cobra.Command{
	Use:   "instance",
	Short: "Manage COS instances (service instances under Resource Controller)",
}

var cosInstanceCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a COS instance",
	Long: `Create a COS service instance under the workspace's resource group.

--plan accepts a friendly name (standard | lite); --plan-id takes a
catalog UUID directly when IBM ships a tier roksbnkctl hasn't mapped yet.
--target defaults to "global" (COS instances are global; buckets carry
the regional affinity).`,
	Args: cobra.ExactArgs(1),
	RunE: runCOSInstanceCreate,
}

var cosInstanceDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a COS instance (and its bound resources unless --no-recursive)",
	Args:  cobra.ExactArgs(1),
	RunE:  runCOSInstanceDelete,
}

var cosInstanceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List COS instances in the current account",
	RunE:  runCOSInstanceList,
}

// ── bucket ──────────────────────────────────────────────────────────

var cosBucketCmd = &cobra.Command{
	Use:   "bucket",
	Short: "Manage COS buckets",
}

var cosBucketCreateCmd = &cobra.Command{
	Use:   "create <bucket>",
	Short: "Create a bucket on the named instance",
	Args:  cobra.ExactArgs(1),
	RunE:  runCOSBucketCreate,
}

var cosBucketDeleteCmd = &cobra.Command{
	Use:   "delete <bucket>",
	Short: "Delete a bucket (must be empty)",
	Args:  cobra.ExactArgs(1),
	RunE:  runCOSBucketDelete,
}

var cosBucketListCmd = &cobra.Command{
	Use:   "list",
	Short: "List buckets on the named instance",
	RunE:  runCOSBucketList,
}

var cosBucketGetCmd = &cobra.Command{
	Use:   "get <bucket> <local-dir>",
	Short: "Recursively download every object in a bucket to a local directory",
	Long: `Recursively download every object in <bucket> to <local-dir>.

<local-dir> is created if it does not exist (mkdir -p semantics, mode
0755). Object keys map to nested subdirectories — a key foo/bar/baz.json
lands at <local-dir>/foo/bar/baz.json. Streaming download per object
(no whole-object in-memory buffering); text and binary are both copied
through verbatim.

Default behavior is overwrite (the operator just asked to download);
pass --no-clobber to skip objects whose local target already exists,
matching cp -n.

With --output json, emits one JSON object per file completed:
{"key","local_path","size","outcome"}. The final counts line goes to
stderr in text mode; the JSON stream concludes with one summary record
of shape {"counts":{...}}.`,
	Args: cobra.ExactArgs(2),
	RunE: runCOSBucketGet,
}

// ── object ──────────────────────────────────────────────────────────

var cosObjectCmd = &cobra.Command{
	Use:   "object",
	Short: "Manage objects in COS buckets",
}

var cosObjectPutCmd = &cobra.Command{
	Use:   "put <bucket>/<key> <local-file>",
	Short: "Upload an object (multipart for large files, streaming)",
	Args:  cobra.ExactArgs(2),
	RunE:  runCOSObjectPut,
}

var cosObjectGetCmd = &cobra.Command{
	Use:   "get <bucket>/<key> <local-file>",
	Short: "Download an object (streaming)",
	Args:  cobra.ExactArgs(2),
	RunE:  runCOSObjectGet,
}

var cosObjectDeleteCmd = &cobra.Command{
	Use:   "delete <bucket>/<key>",
	Short: "Delete an object",
	Args:  cobra.ExactArgs(1),
	RunE:  runCOSObjectDelete,
}

var cosObjectListCmd = &cobra.Command{
	Use:   "list <bucket>[/<prefix>]",
	Short: "List objects (optionally under a prefix)",
	Args:  cobra.ExactArgs(1),
	RunE:  runCOSObjectList,
}

func init() {
	cosInstanceCreateCmd.Flags().StringVar(&flagCOSPlan, "plan", "standard", "service plan name (standard | lite)")
	cosInstanceCreateCmd.Flags().StringVar(&flagCOSPlanID, "plan-id", "", "service plan UUID (overrides --plan; for plans roksbnkctl hasn't mapped)")
	cosInstanceCreateCmd.Flags().StringVar(&flagCOSTarget, "target", "global", "target region (default: global; COS instances are global)")

	cosInstanceDeleteCmd.Flags().BoolVar(&flagAuto, "auto", false, "skip the confirmation prompt")
	cosInstanceDeleteCmd.Flags().BoolVar(&flagCOSRecursive, "no-recursive", false, "do NOT delete bound resources (HMAC keys, service credentials)")

	cosBucketCmd.PersistentFlags().StringVar(&flagCOSInstance, "instance", "", "COS instance name or CRN (required)")
	cosBucketCreateCmd.Flags().StringVar(&flagCOSRegion, "region", "", "bucket region (default: workspace region)")
	cosBucketCreateCmd.Flags().StringVar(&flagCOSClass, "class", "standard", "storage class (standard, vault, cold, smart)")
	cosBucketGetCmd.Flags().BoolVar(&flagCOSNoClobber, "no-clobber", false, "skip objects whose local target already exists (cp -n semantics)")

	cosObjectCmd.PersistentFlags().StringVar(&flagCOSInstance, "instance", "", "COS instance name or CRN (required)")

	cosInstanceCmd.AddCommand(cosInstanceCreateCmd, cosInstanceDeleteCmd, cosInstanceListCmd)
	cosBucketCmd.AddCommand(cosBucketCreateCmd, cosBucketDeleteCmd, cosBucketListCmd, cosBucketGetCmd)
	cosObjectCmd.AddCommand(cosObjectPutCmd, cosObjectGetCmd, cosObjectDeleteCmd, cosObjectListCmd)

	cosCmd.AddCommand(cosInstanceCmd, cosBucketCmd, cosObjectCmd)
	rootCmd.AddCommand(cosCmd)
}

// ── runE implementations ────────────────────────────────────────────

func runCOSInstanceCreate(cmd *cobra.Command, args []string) error {
	name := args[0]
	cctx, ic, err := openIBMClient()
	if err != nil {
		return err
	}

	rgName := cctx.Workspace.IBMCloud.ResourceGroup
	if rgName == "" {
		rgName = "default"
	}
	rgID, err := ic.ResolveResourceGroup(cmd.Context(), rgName)
	if err != nil {
		return fmt.Errorf("resolving resource group %q: %w", rgName, err)
	}

	plan := flagCOSPlan
	if flagCOSPlanID != "" {
		plan = flagCOSPlanID
	}

	fmt.Fprintf(os.Stderr, "→ Creating COS instance %q (plan=%s, rg=%s, target=%s)\n",
		name, plan, rgName, flagCOSTarget)
	inst, err := ic.CreateCOSInstance(cmd.Context(), name, rgID, plan, flagCOSTarget)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "✓ Created %s\n  CRN: %s\n", inst.Name, inst.CRN)
	return nil
}

func runCOSInstanceDelete(cmd *cobra.Command, args []string) error {
	name := args[0]
	_, ic, err := openIBMClient()
	if err != nil {
		return err
	}

	idOrCRN := name
	if !strings.HasPrefix(name, "crn:v1:") {
		inst, err := ic.GetCOSInstanceByName(cmd.Context(), name)
		if err != nil {
			return err
		}
		idOrCRN = inst.CRN
	}

	if !flagAuto {
		if !promptYesNo(fmt.Sprintf("Delete COS instance %q?", name), false) {
			return errCOSAborted
		}
	}

	fmt.Fprintf(os.Stderr, "→ Deleting COS instance %s\n", name)
	// flagCOSRecursive is the "no-recursive" flag — invert.
	if err := ic.DeleteCOSInstance(cmd.Context(), idOrCRN, !flagCOSRecursive); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "✓ Deleted")
	return nil
}

// errCOSAborted is the sentinel for user-cancelled COS operations. Kept
// as a package-level var so cobra's RunE returns it consistently and
// callers (tests, scripts) can compare with errors.Is.
var errCOSAborted = errors.New("aborted")

func runCOSInstanceList(cmd *cobra.Command, _ []string) error {
	_, ic, err := openIBMClient()
	if err != nil {
		return err
	}
	insts, err := ic.ListCOSInstances(cmd.Context())
	if err != nil {
		return err
	}
	if len(insts) == 0 {
		fmt.Fprintln(os.Stderr, "(no COS instances in account)")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tGUID\tSTATE")
	for _, in := range insts {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", in.Name, in.GUID, in.State)
	}
	return tw.Flush()
}

func runCOSBucketCreate(cmd *cobra.Command, args []string) error {
	cc, err := openCOSClient(cmd.Context())
	if err != nil {
		return err
	}
	bucket := args[0]
	fmt.Fprintf(os.Stderr, "→ Creating bucket %s (class %s)\n", bucket, flagCOSClass)
	return cc.CreateBucket(cmd.Context(), bucket, flagCOSClass)
}

func runCOSBucketDelete(cmd *cobra.Command, args []string) error {
	cc, err := openCOSClient(cmd.Context())
	if err != nil {
		return err
	}
	return cc.DeleteBucket(cmd.Context(), args[0])
}

func runCOSBucketList(cmd *cobra.Command, _ []string) error {
	cc, err := openCOSClient(cmd.Context())
	if err != nil {
		return err
	}
	names, err := cc.ListBuckets(cmd.Context())
	if err != nil {
		return err
	}
	for _, n := range names {
		fmt.Println(n)
	}
	return nil
}

func runCOSBucketGet(cmd *cobra.Command, args []string) error {
	bucket := args[0]
	destDir := args[1]
	// openCOSClient enforces --instance and resolves it to a CRN; if
	// --instance is missing the error text matches the other cos bucket
	// verbs (acceptance criterion 6) because it comes out of the same
	// helper they all share.
	if flagCOSInstance == "" {
		return fmt.Errorf("--instance is required (name or CRN)")
	}
	cc, err := openCOSClient(cmd.Context())
	if err != nil {
		return err
	}

	jsonMode := flagOutput == "json"
	enc := json.NewEncoder(os.Stdout)

	// onItem is invoked once per object after its outcome is known.
	// Text mode: one line per file to stderr (kept off stdout so the
	// `--output text` channel stays free for piping). JSON mode: one
	// JSON record per file streamed to stdout, terminated by a summary
	// record below.
	onItem := func(it cos.GetBucketItem) {
		if jsonMode {
			_ = enc.Encode(it)
			return
		}
		if flagQuiet {
			return
		}
		switch it.Outcome {
		case "skipped":
			fmt.Fprintf(os.Stderr, "  skip %s (exists)\n", it.LocalPath)
		default:
			fmt.Fprintf(os.Stderr, "  get  %s/%s → %s (%d bytes)\n", bucket, it.Key, it.LocalPath, it.Size)
		}
	}

	if !jsonMode && !flagQuiet {
		fmt.Fprintf(os.Stderr, "→ Downloading bucket %s to %s\n", bucket, destDir)
	}

	opts := cos.ClientGetBucketOptions(cc)
	opts.NoClobber = flagCOSNoClobber
	opts.OnItem = onItem

	counts, err := cos.GetBucket(cmd.Context(), flagCOSInstance, bucket, destDir, opts)
	if err != nil {
		return err
	}

	if jsonMode {
		// Trailing summary record so JSON consumers can pick up final
		// counters without parsing stderr.
		_ = enc.Encode(struct {
			Counts cos.GetBucketCounts `json:"counts"`
		}{Counts: counts})
		return nil
	}

	if counts.Objects == 0 && counts.Skipped == 0 {
		fmt.Fprintln(os.Stderr, "no objects in bucket")
		return nil
	}
	if !flagQuiet {
		fmt.Fprintf(os.Stderr, "✓ %d objects, %d bytes, %d skipped (no-clobber)\n",
			counts.Objects, counts.Bytes, counts.Skipped)
	}
	return nil
}

func runCOSObjectPut(cmd *cobra.Command, args []string) error {
	bucket, key, ok := splitBucketKey(args[0])
	if !ok {
		return fmt.Errorf("expected <bucket>/<key>, got %q", args[0])
	}
	cc, err := openCOSClient(cmd.Context())
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "→ Uploading %s to %s/%s\n", args[1], bucket, key)
	if err := cc.PutObjectFromFile(cmd.Context(), bucket, key, args[1]); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "✓ uploaded")
	return nil
}

func runCOSObjectGet(cmd *cobra.Command, args []string) error {
	bucket, key, ok := splitBucketKey(args[0])
	if !ok {
		return fmt.Errorf("expected <bucket>/<key>, got %q", args[0])
	}
	cc, err := openCOSClient(cmd.Context())
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "→ Downloading %s/%s to %s\n", bucket, key, args[1])
	if err := cc.GetObjectToFile(cmd.Context(), bucket, key, args[1]); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "✓ downloaded")
	return nil
}

func runCOSObjectDelete(cmd *cobra.Command, args []string) error {
	bucket, key, ok := splitBucketKey(args[0])
	if !ok {
		return fmt.Errorf("expected <bucket>/<key>, got %q", args[0])
	}
	cc, err := openCOSClient(cmd.Context())
	if err != nil {
		return err
	}
	return cc.DeleteObject(cmd.Context(), bucket, key)
}

func runCOSObjectList(cmd *cobra.Command, args []string) error {
	bucket, prefix, _ := strings.Cut(args[0], "/")
	cc, err := openCOSClient(cmd.Context())
	if err != nil {
		return err
	}
	objs, err := cc.ListObjects(cmd.Context(), bucket, prefix)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KEY\tSIZE\tMODIFIED")
	for _, o := range objs {
		fmt.Fprintf(tw, "%s\t%d\t%s\n", o.Key, o.Size, o.LastModified.Format("2006-01-02T15:04:05Z"))
	}
	return tw.Flush()
}

// ── helpers ─────────────────────────────────────────────────────────

// openIBMClient resolves the workspace's API key + region and returns
// an *ibm.Client. Used by commands that operate on Resource Controller
// or IAM (not S3 directly).
func openIBMClient() (*config.Context, *ibm.Client, error) {
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return nil, nil, err
	}
	if cctx.Workspace == nil {
		return nil, nil, fmt.Errorf("workspace %q is not initialised; run `roksbnkctl init` first", cctx.WorkspaceName)
	}
	resolver := &cred.Resolver{
		Workspace: cctx.WorkspaceName,
		Source:    cctx.Workspace.IBMCloud.APIKeySource,
	}
	apiKey, err := resolver.IBMCloudAPIKey(context.Background())
	if err != nil {
		return nil, nil, fmt.Errorf("resolving API key: %w", err)
	}
	ic, err := ibm.New(apiKey, cctx.Workspace.IBMCloud.Region)
	if err != nil {
		return nil, nil, err
	}
	return cctx, ic, nil
}

// openCOSClient resolves --instance into a CRN and returns a *cos.Client
// scoped to that instance + the workspace region.
func openCOSClient(ctx context.Context) (*cos.Client, error) {
	if flagCOSInstance == "" {
		return nil, fmt.Errorf("--instance is required (name or CRN)")
	}
	cctx, ic, err := openIBMClient()
	if err != nil {
		return nil, err
	}
	crn, err := resolveCOSInstance(ctx, ic, flagCOSInstance)
	if err != nil {
		return nil, err
	}
	return cos.New(ic.APIKey(), cctx.Workspace.IBMCloud.Region, crn)
}

// resolveCOSInstance accepts either a CRN (used as-is) or a name (looked
// up via Resource Controller).
func resolveCOSInstance(ctx context.Context, ic *ibm.Client, arg string) (string, error) {
	if strings.HasPrefix(arg, "crn:v1:") {
		return arg, nil
	}
	inst, err := ic.GetCOSInstanceByName(ctx, arg)
	if err != nil {
		return "", err
	}
	return inst.CRN, nil
}

// splitBucketKey splits "bucket/key/with/slashes" into ("bucket", "key/with/slashes", true).
// Empty bucket or empty key returns false.
func splitBucketKey(s string) (bucket, key string, ok bool) {
	i := strings.Index(s, "/")
	if i <= 0 || i == len(s)-1 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}
