package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/cos"
	"github.com/jgruberf5/roksbnkctl/internal/ibm"
)

// ensureFARSupplyChain runs at interactive `init` time (only when BNK is being
// deployed). It checks the orchestration COS holds what the BNK phase needs —
// the FAR auth tarball and the subscription JWT — and, when something is
// missing, offers to provision it from local files: create the COS instance and
// bucket if absent and upload the two objects. The coordinates it provisions
// are recorded in ws.COS so the BNK phase and `registry` resolve from exactly
// what was created.
//
// It never blocks init: declining (or a resolvable-but-empty check) just prints
// a warning and returns nil, leaving the operator to populate COS by hand.
func ensureFARSupplyChain(ctx context.Context, ic *ibm.Client, apiKey, rgID string, ws *config.Workspace) error {
	// Already pinned to local files (a prior init, or set by hand): honour them and
	// skip COS entirely — no instance/bucket/DNS needed.
	if ws.BNK.FarAuthLocalFile != "" && ws.BNK.SubscriptionJWTLocalFile != "" {
		fmt.Fprintf(os.Stderr, "\n→ FAR supply chain: using local files (COS not consulted)\n    FAR: %s\n    JWT: %s\n",
			ws.BNK.FarAuthLocalFile, ws.BNK.SubscriptionJWTLocalFile)
		return nil
	}

	farFile := ws.BNK.FarAuthFile
	if farFile == "" {
		farFile = config.DefaultFARAuthFile
	}
	jwtFile := ws.BNK.SubscriptionJWTFile
	if jwtFile == "" {
		jwtFile = config.DefaultSubscriptionJWTFile
	}

	// The orchestration COS lives in a fixed region (DefaultCOSRegion), NOT the
	// cluster's region — a cluster region like eu-fr2 has no COS S3 endpoint, so
	// defaulting the bucket region to it produced "no such host". Only an explicit
	// cos.region overrides the default.
	instName, bucket, bucketRegion := config.DefaultCOSInstance, config.DefaultCOSBucket, config.DefaultCOSRegion
	if ws.COS != nil {
		if ws.COS.Instance != "" {
			instName = ws.COS.Instance
		}
		if ws.COS.Bucket != "" {
			bucket = ws.COS.Bucket
		}
		if ws.COS.Region != "" {
			bucketRegion = ws.COS.Region
		}
	}
	// Whether the operator pinned an exact bucket name. A pinned name is respected
	// as-is; the generic default gets a global-uniqueness suffix when provisioned.
	bucketExplicit := ws.COS != nil && ws.COS.Bucket != ""

	// Account-scoped unique-bucket candidate. Every workspace run from the same
	// account's API key derives the SAME name, so a second workspace discovers +
	// reuses the bucket the first one provisioned. Empty for an explicit name.
	acctID := ""
	if id := ic.Identity(); id != nil {
		acctID = id.AccountID
	}
	altBucket := ""
	if !bucketExplicit {
		altBucket = uniqueBucketName(bucket, acctID)
	}

	fmt.Fprintf(os.Stderr, "\n→ Checking the FAR supply chain (COS instance %q / bucket %q → %s + %s)\n",
		instName, bucket, farFile, jwtFile)

	// Probe COS. Any transport/DNS/auth error is NON-FATAL: we fall back to local
	// files rather than aborting init (which used to leave no workspace at all).
	// resolvedBucket is whichever candidate (the plain name or the account-suffixed
	// one) actually exists — so a workspace discovers a previously-provisioned bucket.
	inst, cc, resolvedBucket, bucketOK, farOK, jwtOK, cosErr := checkCOSSupplyChain(ctx, ic, apiKey, instName, bucket, altBucket, bucketRegion, farFile, jwtFile)
	bucket = resolvedBucket
	if cosErr == nil && inst != nil && bucketOK && farOK && jwtOK {
		// Record the DISCOVERED coordinates so the BNK phase resolves from exactly
		// this bucket (the suffixed name, not the bare default).
		recordCOSCoords(ws, instName, bucket, bucketRegion)
		fmt.Fprintln(os.Stderr, "✓ FAR supply chain is available")
		return nil
	}

	if cosErr != nil {
		fmt.Fprintf(os.Stderr, "⚠ COS supply-chain check failed: %v\n", cosErr)
		fmt.Fprintln(os.Stderr, "  Falling back to local files (no COS needed).")
		return promptLocalSupplyChain(ws, farFile, jwtFile)
	}

	// COS reachable but something is missing — report it, then prefer local files
	// (avoids COS entirely); the operator can still choose to provision + upload.
	var missing []string
	if inst == nil {
		missing = append(missing, fmt.Sprintf("COS instance %q", instName))
	} else if !bucketOK {
		missing = append(missing, fmt.Sprintf("bucket %q", bucket))
	}
	if !farOK {
		missing = append(missing, farFile)
	}
	if !jwtOK {
		missing = append(missing, jwtFile)
	}
	fmt.Fprintf(os.Stderr, "  Missing: %s\n", strings.Join(missing, ", "))

	if promptYesNo("Use local files for the FAR auth tarball + subscription JWT (no COS)?", true) {
		return promptLocalSupplyChain(ws, farFile, jwtFile)
	}
	if !promptYesNo("Provision the COS supply chain instead (create instance/bucket + upload)?", false) {
		fmt.Fprintln(os.Stderr, "  Skipped — the BNK phase will fail until the FAR auth tarball and JWT are available.")
		return nil
	}

	farPath := promptExistingFile(fmt.Sprintf("Path to the FAR auth tarball (uploaded as %s)", farFile))
	jwtPath := promptExistingFile(fmt.Sprintf("Path to the subscription JWT (uploaded as %s)", jwtFile))

	if inst == nil {
		fmt.Fprintf(os.Stderr, "→ Creating COS instance %q (plan=standard)\n", instName)
		cctx, ccancel := apiCtx(ctx)
		created, cerr := ic.CreateCOSInstance(cctx, instName, rgID, "standard", "global")
		ccancel()
		if cerr != nil {
			return fmt.Errorf("creating COS instance %q: %w", instName, cerr)
		}
		inst = created
		newCC, nerr := cos.New(apiKey, bucketRegion, inst.CRN)
		if nerr != nil {
			return fmt.Errorf("opening COS client: %w", nerr)
		}
		cc = newCC
		cc.WithResolver(cos.DefaultBucketRegionResolver(cc))
		bucketOK = false
	}

	if !bucketOK {
		// COS bucket names share ONE global namespace (like S3), so the generic
		// default ("bnk-artifacts") frequently collides with a bucket another
		// account already owns ("BucketAlreadyExists"). Provision under a
		// globally-unique, account-stable name derived from the instance; an
		// explicitly-configured bucket name is used as-is.
		if !bucketExplicit {
			bucket = uniqueBucketName(bucket, acctID)
		}
		// A prior interrupted run may already have created it (ours) — reuse it.
		lctx, lcancel := apiCtx(ctx)
		buckets, _ := cc.ListBuckets(lctx)
		lcancel()
		exists := false
		for _, b := range buckets {
			if b == bucket {
				exists = true
				break
			}
		}
		if exists {
			fmt.Fprintf(os.Stderr, "→ Reusing existing bucket %q\n", bucket)
		} else {
			fmt.Fprintf(os.Stderr, "→ Creating bucket %q (class standard, region %s)\n", bucket, bucketRegion)
			bctx, bcancel := apiCtx(ctx)
			berr := cc.CreateBucket(bctx, bucket, "standard")
			bcancel()
			if berr != nil {
				return fmt.Errorf("creating bucket %q: %w", bucket, berr)
			}
		}
	}

	for _, up := range []struct{ path, key string }{{farPath, farFile}, {jwtPath, jwtFile}} {
		fmt.Fprintf(os.Stderr, "→ Uploading %s → %s/%s\n", up.path, bucket, up.key)
		uctx, ucancel := apiCtx(ctx)
		uerr := cc.PutObjectFromFile(uctx, bucket, up.key, up.path)
		ucancel()
		if uerr != nil {
			return fmt.Errorf("uploading %s: %w", up.key, uerr)
		}
	}

	// Record the coordinates we provisioned so the BNK phase + `registry` resolve
	// from exactly here (the bucket name in particular is the account-suffixed one).
	recordCOSCoords(ws, instName, bucket, bucketRegion)

	fmt.Fprintln(os.Stderr, "✓ FAR supply chain ready")
	return nil
}

// recordCOSCoords persists the resolved orchestration-COS coordinates on the
// workspace (only filling empty fields, so an explicit cos: block wins), so the
// BNK phase + `registry` resolve from exactly the instance/bucket/region used here.
func recordCOSCoords(ws *config.Workspace, instName, bucket, region string) {
	if ws.COS == nil {
		ws.COS = &config.COSCfg{}
	}
	if ws.COS.Instance == "" {
		ws.COS.Instance = instName
	}
	if ws.COS.Bucket == "" {
		ws.COS.Bucket = bucket
	}
	if ws.COS.Region == "" {
		ws.COS.Region = region
	}
}

// uniqueBucketName derives a globally-unique bucket name from base by appending a
// short suffix from the ACCOUNT ID. IBM COS bucket names share one global namespace
// (like S3), so a generic base such as "bnk-artifacts" often collides with a bucket
// another account already owns. Keying the suffix off the account ID (not the
// instance) makes it:
//   - globally unique (account IDs are unique), and
//   - STABLE and DISCOVERABLE across workspaces: every workspace run from the same
//     account's API key derives the same name, so a second workspace finds and
//     reuses the bucket the first one provisioned.
//
// Returns base unchanged when the account ID is unavailable.
func uniqueBucketName(base, accountID string) string {
	token := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return -1
		}
	}, accountID)
	if len(token) > 12 {
		token = token[:12]
	}
	if token == "" {
		return base
	}
	return base + "-" + token
}

// checkCOSSupplyChain probes the orchestration COS for the instance, the bucket
// (either the plain name OR the account-suffixed altBucket — whichever exists, so
// a previously-provisioned bucket is DISCOVERED), and the two objects. It returns
// the resolvedBucket actually found. A non-nil error means COS itself was
// unreachable/unusable (transport, DNS, auth) — the caller falls back to local
// files. A nil error with a false flag means COS is reachable but the artefact is
// simply absent.
func checkCOSSupplyChain(ctx context.Context, ic *ibm.Client, apiKey, instName, bucket, altBucket, bucketRegion, farFile, jwtFile string) (inst *ibm.COSInstance, cc *cos.Client, resolvedBucket string, bucketOK, farOK, jwtOK bool, cosErr error) {
	resolvedBucket = bucket
	lctx, lcancel := apiCtx(ctx)
	insts, err := ic.ListCOSInstances(lctx)
	lcancel()
	if err != nil {
		return nil, nil, resolvedBucket, false, false, false, fmt.Errorf("listing COS instances: %w", err)
	}
	for i := range insts {
		if insts[i].Name == instName {
			inst = &insts[i]
			break
		}
	}
	if inst == nil {
		return nil, nil, resolvedBucket, false, false, false, nil // reachable, instance simply absent
	}

	cc, err = cos.New(apiKey, bucketRegion, inst.CRN)
	if err != nil {
		return inst, nil, resolvedBucket, false, false, false, fmt.Errorf("opening COS client: %w", err)
	}
	cc.WithResolver(cos.DefaultBucketRegionResolver(cc))

	bctx, bcancel := apiCtx(ctx)
	buckets, berr := cc.ListBuckets(bctx)
	bcancel()
	if berr != nil {
		return inst, cc, resolvedBucket, false, false, false, fmt.Errorf("listing buckets in %q: %w", instName, berr)
	}
	inList := func(name string) bool {
		if name == "" {
			return false
		}
		for _, b := range buckets {
			if b == name {
				return true
			}
		}
		return false
	}
	// Prefer the plain name; otherwise discover the account-suffixed one.
	switch {
	case inList(bucket):
		resolvedBucket, bucketOK = bucket, true
	case inList(altBucket):
		resolvedBucket, bucketOK = altBucket, true
	}
	if bucketOK {
		octx, ocancel := apiCtx(ctx)
		farOK = objectExists(octx, cc, resolvedBucket, farFile)
		jwtOK = objectExists(octx, cc, resolvedBucket, jwtFile)
		ocancel()
	}
	return inst, cc, resolvedBucket, bucketOK, farOK, jwtOK, nil
}

// promptLocalSupplyChain records LOCAL file paths for the FAR tarball + JWT on the
// workspace (bnk.far_auth_local_file / subscription_jwt_local_file). The BNK phase
// then reads them directly — no COS. Suggested filenames match the COS keys so the
// operator recognises the artefacts.
func promptLocalSupplyChain(ws *config.Workspace, farFile, jwtFile string) error {
	farPath := promptExistingFile(fmt.Sprintf("Path to the local FAR auth tarball (%s)", farFile))
	jwtPath := promptExistingFile(fmt.Sprintf("Path to the local subscription JWT (%s)", jwtFile))
	ws.BNK.FarAuthLocalFile = farPath
	ws.BNK.SubscriptionJWTLocalFile = jwtPath
	fmt.Fprintf(os.Stderr, "✓ FAR supply chain: using local files\n    FAR: %s\n    JWT: %s\n", farPath, jwtPath)
	return nil
}

// objectExists reports whether an object with the exact key exists in bucket.
// A list error is treated as "absent" — the caller then offers to (re)create,
// and the subsequent upload surfaces any real access error with context.
func objectExists(ctx context.Context, cc *cos.Client, bucket, key string) bool {
	objs, err := cc.ListObjects(ctx, bucket, key)
	if err != nil {
		return false
	}
	for _, o := range objs {
		if o.Key == key {
			return true
		}
	}
	return false
}

// promptExistingFile re-prompts until the operator gives a path to a readable
// file (expanding a leading ~). There is no default — a supply-chain upload
// needs a real artefact.
func promptExistingFile(label string) string {
	for {
		p := expandHomePath(strings.TrimSpace(promptString(label, "")))
		if p == "" {
			fmt.Fprintln(os.Stderr, "  (a path is required)")
			continue
		}
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
		fmt.Fprintf(os.Stderr, "  Not a readable file: %s\n", p)
	}
}

// expandHomePath expands a leading ~/ to the user's home directory.
func expandHomePath(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	return p
}
