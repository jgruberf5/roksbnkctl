package tf

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	roksbnkctl "github.com/jgruberf5/roksbnkctl"
	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// FetchSource resolves a TFSourceCfg into a local directory containing
// the .tf files terraform will operate on.
//
// type=embedded (or empty) — extracts the bundled ./terraform/ tree
// from the binary's go:embed FS into baseDir/embedded-terraform/ and
// returns that path. Default for new workspaces; means a fresh
// `roksbnkctl up` works without any network access for the source.
//
// type=local — uses Path directly (verified to exist + be a dir).
//
// type=github — downloads the release tarball into baseDir/<repo-leaf>-<ref>/
// and returns that path. Idempotent: if the dir already exists with
// content, just returns it without re-downloading.
//
// stripping: GitHub tarballs have a single top-level dir (e.g.
// "ibmcloud_terraform_bigip_next_for_kubernetes_2_3-0.6.7/"); we strip
// it so the .tf files land directly under the dest.
func FetchSource(ctx context.Context, src config.TFSourceCfg, baseDir string) (string, error) {
	return FetchSourceForLine(ctx, src, baseDir, "")
}

// FetchSourceForLine is FetchSource with the BNK release line the source is
// being fetched FOR.
//
// Only the embedded source varies by line — a local path or a GitHub ref is
// already a specific tree the user chose, and silently layering onto it would
// mean they are not running what they pointed at.
//
// An empty line, or a line with no overlay, extracts the base tree unchanged.
// That is the normal case and the one every existing workspace takes.
func FetchSourceForLine(ctx context.Context, src config.TFSourceCfg, baseDir, line string) (string, error) {
	switch src.Type {
	case "", "embedded":
		if baseDir == "" {
			return "", fmt.Errorf("baseDir is empty (where should the embedded source be extracted?)")
		}
		return extractEmbeddedTF(baseDir, line)

	case "local":
		if src.Path == "" {
			return "", fmt.Errorf("local TF source has empty path")
		}
		// Self-heal: a config.yaml written before the init-time
		// normalization (internal/cli/init.go::resolveLocalTFSource)
		// may carry a *relative* path. terraform-exec runs with
		// CWD = per-phase state dir, so a relative source path would
		// resolve there instead of where the user ran `init`. Absolutize
		// here so legacy configs keep working; init.go already pins
		// absolute for freshly-written ones, making this idempotent.
		path := src.Path
		if !filepath.IsAbs(path) {
			abs, err := filepath.Abs(path)
			if err != nil {
				return "", fmt.Errorf("local TF source %s: %w", path, err)
			}
			path = abs
		}
		info, err := os.Stat(path)
		if err != nil {
			return "", fmt.Errorf("local TF source %s: %w", path, err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("local TF source %s is not a directory", path)
		}
		return path, nil

	case "github":
		if src.Repo == "" || src.Ref == "" {
			return "", fmt.Errorf("github TF source needs both repo and ref (got repo=%q ref=%q)", src.Repo, src.Ref)
		}
		if baseDir == "" {
			return "", fmt.Errorf("baseDir is empty (where should the source be downloaded?)")
		}
		return downloadGitHubTarball(ctx, src.Repo, src.Ref, baseDir)

	default:
		return "", fmt.Errorf("unknown TF source type %q (want embedded, github, or local)", src.Type)
	}
}

// extractEmbeddedTF walks the bundled go:embed FS and writes its files
// into baseDir/embedded-terraform/. Re-extracts on every invocation so
// a binary upgrade picks up new HCL — embed.FS file sizes are tiny vs
// roksbnkctl's overall startup cost so the redundant write is fine.
//
// Returns the resolved source dir for terraform-exec.
func extractEmbeddedTF(baseDir, line string) (string, error) {
	dest := filepath.Join(baseDir, "embedded-terraform")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", dest, err)
	}
	cleanDest := filepath.Clean(dest)

	err := fs.WalkDir(roksbnkctl.EmbeddedTerraform, "terraform", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		// Strip the top-level "terraform/" prefix so files land
		// directly under dest/ — same shape as github fetch.
		rel := strings.TrimPrefix(path, "terraform")
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			return nil
		}
		// Defensive: never materialise a `.terraform/` provider/module
		// cache from the embed. The embed directive already excludes it
		// (plain `//go:embed terraform` skips dotfiles), but if it ever
		// creeps back in (e.g. a switch to `all:`), extracting those
		// plugin binaries — written 0644 below — would ship
		// non-executable providers and break `terraform plan` with
		// "fork/exec ... permission denied". Let `terraform init` build a
		// clean, executable .terraform instead.
		if rel == ".terraform" || strings.HasPrefix(rel, ".terraform/") ||
			strings.Contains(rel, "/.terraform/") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		// The per-line overlays are not part of the base tree — they are
		// applied on top of it below, and only the one for this release.
		// Extracting them all would leave every other line's HCL sitting in
		// the module tree.
		if rel == overlayRoot || strings.HasPrefix(rel, overlayRoot+"/") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		target := filepath.Join(cleanDest, rel)
		if !strings.HasPrefix(target, cleanDest+string(os.PathSeparator)) && target != cleanDest {
			return fmt.Errorf("embed entry escapes destination: %s", path)
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		body, err := fs.ReadFile(roksbnkctl.EmbeddedTerraform, path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		// The lockfile is a SEED, not an authority. Every other file here is
		// ours and is rewritten on each run so a binary upgrade picks up new
		// HCL; the lockfile is terraform's, and after the first `init` the copy
		// in this directory is the workspace's own — evolved by terraform,
		// recording exactly what that workspace installed.
		//
		// Clobbering it would silently downgrade a live workspace's providers
		// on the first run after a binary upgrade, and terraform.go inits with
		// Upgrade(false), so it could never self-heal. Downgrading below the
		// provider that wrote the state is a hard failure ("Resource instance
		// managed by newer provider version"), with no way out from inside the
		// tool.
		//
		// So: seed a fresh workspace with the pinned, multi-platform set, and
		// leave an established one alone. A workspace that wants the newer
		// baseline deletes its lockfile or runs `terraform init -upgrade` — the
		// same answer terraform gives everywhere else.
		if filepath.Base(target) == ".terraform.lock.hcl" {
			if _, serr := os.Stat(target); serr == nil {
				return nil
			}
		}
		return os.WriteFile(target, body, 0o644)
	})
	if err != nil {
		return "", fmt.Errorf("extracting embedded terraform: %w", err)
	}
	if err := applyLineOverlay(roksbnkctl.EmbeddedTerraform, cleanDest, line); err != nil {
		return "", err
	}
	return dest, nil
}

// overlayRoot is where per-line HCL lives inside the embedded tree, relative to
// terraform/. See terraform/lines/README.md.
const overlayRoot = "lines"

// applyLineOverlay writes terraform/lines/<line>/ over an already-extracted base
// tree: same relative path replaces, new path is added, nothing is removed.
//
// A missing overlay is the NORMAL case, not an error — most releases are served
// by the base tree, and treating "no overlay" as a failure would make adding the
// mechanism a breaking change for every line that does not need it.
func applyLineOverlay(srcFS fs.FS, destRoot, line string) error {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	// The line is derived from a version string rather than typed by a user, but
	// it still ends up in a filesystem path, so it does not get to contain one.
	if strings.ContainsAny(line, `/\`) || line == "." || line == ".." {
		return fmt.Errorf("refusing to use %q as a terraform overlay name", line)
	}

	root := overlayRoot + "/" + line
	src := "terraform/" + root
	if _, err := fs.Stat(srcFS, src); err != nil {
		return nil // no overlay for this line — base tree stands alone
	}

	return fs.WalkDir(srcFS, src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(path, src), "/")
		if rel == "" {
			return nil
		}
		target := filepath.Join(destRoot, rel)
		if !strings.HasPrefix(target, destRoot+string(os.PathSeparator)) {
			return fmt.Errorf("overlay entry escapes destination: %s", path)
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		// The overlay's own README documents the mechanism for maintainers; it
		// is not HCL and has no business in an extracted module tree.
		if rel == "README.md" {
			return nil
		}
		body, err := fs.ReadFile(srcFS, path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		// The lockfile is a SEED, not an authority. Every other file here is
		// ours and is rewritten on each run so a binary upgrade picks up new
		// HCL; the lockfile is terraform's, and after the first `init` the copy
		// in this directory is the workspace's own — evolved by terraform,
		// recording exactly what that workspace installed.
		//
		// Clobbering it would silently downgrade a live workspace's providers
		// on the first run after a binary upgrade, and terraform.go inits with
		// Upgrade(false), so it could never self-heal. Downgrading below the
		// provider that wrote the state is a hard failure ("Resource instance
		// managed by newer provider version"), with no way out from inside the
		// tool.
		//
		// So: seed a fresh workspace with the pinned, multi-platform set, and
		// leave an established one alone. A workspace that wants the newer
		// baseline deletes its lockfile or runs `terraform init -upgrade` — the
		// same answer terraform gives everywhere else.
		if filepath.Base(target) == ".terraform.lock.hcl" {
			if _, serr := os.Stat(target); serr == nil {
				return nil
			}
		}
		return os.WriteFile(target, body, 0o644)
	})
}

// EnsureProvidersExecutable heals a stale <sourceDir>/.terraform/providers
// tree whose provider plugin binaries lack the execute bit. That is the
// artefact of an earlier roksbnkctl build which embedded (via `all:`) and
// extracted the provider cache 0644: terraform "reuses" those
// non-executable binaries on the next init and then fails the plan with
//
//	failed to instantiate provider ...: fork/exec
//	.terraform/providers/.../terraform-provider-X: permission denied
//
// chmod +x is a no-op for the 0755 binaries terraform installs itself, so
// this is safe to run on every Open. Best-effort: a missing dir or a chmod
// error is non-fatal — terraform surfaces its own clearer error if a
// provider genuinely can't run.
func EnsureProvidersExecutable(sourceDir string) {
	provDir := filepath.Join(sourceDir, ".terraform", "providers")
	_ = filepath.WalkDir(provDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // tree may not exist yet — nothing to heal
		}
		if d.IsDir() || !strings.HasPrefix(d.Name(), "terraform-provider-") {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil {
			return nil
		}
		if info.Mode()&0o111 == 0 { // no execute bits at all → heal
			_ = os.Chmod(path, info.Mode()|0o111)
		}
		return nil
	})
}

func downloadGitHubTarball(ctx context.Context, repo, ref, baseDir string) (string, error) {
	leaf := repo
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		leaf = repo[i+1:]
	}
	dest := filepath.Join(baseDir, leaf+"-"+ref)

	// Already present? Reuse — release tags are immutable so re-download
	// would just give us the same bytes.
	if entries, err := os.ReadDir(dest); err == nil && len(entries) > 0 {
		return dest, nil
	}

	url := fmt.Sprintf("https://github.com/%s/archive/refs/tags/%s.tar.gz", repo, ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "roksbnkctl")
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading %s: %s", url, resp.Status)
	}

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", err
	}

	// stripComponents=1: GitHub wraps everything in <repo>-<ref>/.
	if err := extractTarGz(resp.Body, dest, 1); err != nil {
		_ = os.RemoveAll(dest)
		return "", fmt.Errorf("extracting %s: %w", url, err)
	}
	return dest, nil
}

// extractTarGz extracts a gzip'd tarball into dest, stripping the first
// stripComponents leading path components from each entry — equivalent to
// `tar --strip-components=N`.
//
// Defenses: rejects entries that escape dest via "../"; skips symlinks
// (we don't want a tarball pointing at /etc/passwd); ignores anything
// other than regular files and directories.
func extractTarGz(r io.Reader, dest string, stripComponents int) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	cleanDest := filepath.Clean(dest)

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("tar next: %w", err)
		}

		parts := strings.Split(filepath.ToSlash(hdr.Name), "/")
		if len(parts) <= stripComponents {
			continue
		}
		rel := filepath.Join(parts[stripComponents:]...)
		if rel == "" || rel == "." {
			continue
		}

		target := filepath.Join(cleanDest, rel)
		// Guard against ../ traversal.
		if !strings.HasPrefix(target, cleanDest+string(os.PathSeparator)) && target != cleanDest {
			return fmt.Errorf("tar entry escapes destination: %s", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		default:
			// Symlinks, devices, etc. — skip silently.
		}
	}
}
