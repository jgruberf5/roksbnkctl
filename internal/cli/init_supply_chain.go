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

	fmt.Fprintf(os.Stderr, "\n→ Checking the FAR supply chain (COS instance %q / bucket %q → %s + %s)\n",
		instName, bucket, farFile, jwtFile)

	// Probe COS. Any transport/DNS/auth error is NON-FATAL: we fall back to local
	// files rather than aborting init (which used to leave no workspace at all).
	inst, cc, bucketOK, farOK, jwtOK, cosErr := checkCOSSupplyChain(ctx, ic, apiKey, instName, bucket, bucketRegion, farFile, jwtFile)
	if cosErr == nil && inst != nil && bucketOK && farOK && jwtOK {
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
		fmt.Fprintf(os.Stderr, "→ Creating bucket %q (class standard, region %s)\n", bucket, bucketRegion)
		bctx, bcancel := apiCtx(ctx)
		berr := cc.CreateBucket(bctx, bucket, "standard")
		bcancel()
		if berr != nil {
			return fmt.Errorf("creating bucket %q: %w", bucket, berr)
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
	// from exactly here (the region in particular may differ from the default).
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
		ws.COS.Region = bucketRegion
	}

	fmt.Fprintln(os.Stderr, "✓ FAR supply chain ready")
	return nil
}

// checkCOSSupplyChain probes the orchestration COS for the instance, bucket, and
// the two objects. A non-nil error means COS itself was unreachable/unusable
// (transport, DNS, auth) — the caller falls back to local files. A nil error with
// a false flag means COS is reachable but the artefact is simply absent.
func checkCOSSupplyChain(ctx context.Context, ic *ibm.Client, apiKey, instName, bucket, bucketRegion, farFile, jwtFile string) (inst *ibm.COSInstance, cc *cos.Client, bucketOK, farOK, jwtOK bool, cosErr error) {
	lctx, lcancel := apiCtx(ctx)
	insts, err := ic.ListCOSInstances(lctx)
	lcancel()
	if err != nil {
		return nil, nil, false, false, false, fmt.Errorf("listing COS instances: %w", err)
	}
	for i := range insts {
		if insts[i].Name == instName {
			inst = &insts[i]
			break
		}
	}
	if inst == nil {
		return nil, nil, false, false, false, nil // reachable, instance simply absent
	}

	cc, err = cos.New(apiKey, bucketRegion, inst.CRN)
	if err != nil {
		return inst, nil, false, false, false, fmt.Errorf("opening COS client: %w", err)
	}
	cc.WithResolver(cos.DefaultBucketRegionResolver(cc))

	bctx, bcancel := apiCtx(ctx)
	buckets, berr := cc.ListBuckets(bctx)
	bcancel()
	if berr != nil {
		return inst, cc, false, false, false, fmt.Errorf("listing buckets in %q: %w", instName, berr)
	}
	for _, b := range buckets {
		if b == bucket {
			bucketOK = true
			break
		}
	}
	if bucketOK {
		octx, ocancel := apiCtx(ctx)
		farOK = objectExists(octx, cc, bucket, farFile)
		jwtOK = objectExists(octx, cc, bucket, jwtFile)
		ocancel()
	}
	return inst, cc, bucketOK, farOK, jwtOK, nil
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
