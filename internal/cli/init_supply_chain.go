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
	farFile := ws.BNK.FarAuthFile
	if farFile == "" {
		farFile = config.DefaultFARAuthFile
	}
	jwtFile := ws.BNK.SubscriptionJWTFile
	if jwtFile == "" {
		jwtFile = config.DefaultSubscriptionJWTFile
	}

	instName, bucket, bucketRegion := config.DefaultCOSInstance, config.DefaultCOSBucket, ws.IBMCloud.Region
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
	if bucketRegion == "" {
		bucketRegion = config.DefaultCOSRegion
	}

	fmt.Fprintf(os.Stderr, "\n→ Checking the FAR supply chain (COS instance %q / bucket %q → %s + %s)\n",
		instName, bucket, farFile, jwtFile)

	// Instance: list rather than get-by-name so "absent" is a clean nil, not an
	// error we'd have to distinguish from a real failure.
	lctx, lcancel := apiCtx(ctx)
	insts, err := ic.ListCOSInstances(lctx)
	lcancel()
	if err != nil {
		return fmt.Errorf("listing COS instances: %w", err)
	}
	var inst *ibm.COSInstance
	for i := range insts {
		if insts[i].Name == instName {
			inst = &insts[i]
			break
		}
	}

	var cc *cos.Client
	bucketOK, farOK, jwtOK := false, false, false
	if inst != nil {
		cc, err = cos.New(apiKey, bucketRegion, inst.CRN)
		if err != nil {
			return fmt.Errorf("opening COS client: %w", err)
		}
		cc.WithResolver(cos.DefaultBucketRegionResolver(cc))

		bctx, bcancel := apiCtx(ctx)
		buckets, berr := cc.ListBuckets(bctx)
		bcancel()
		if berr != nil {
			return fmt.Errorf("listing buckets in %q: %w", instName, berr)
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
	}

	if inst != nil && bucketOK && farOK && jwtOK {
		fmt.Fprintln(os.Stderr, "✓ FAR supply chain is available")
		return nil
	}

	// Report exactly what's missing.
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

	if !promptYesNo("Create the FAR supply chain now from local files?", true) {
		fmt.Fprintln(os.Stderr, "  Skipped — the BNK phase will fail until the FAR auth tarball and JWT are in COS.")
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
		cc, err = cos.New(apiKey, bucketRegion, inst.CRN)
		if err != nil {
			return fmt.Errorf("opening COS client: %w", err)
		}
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
