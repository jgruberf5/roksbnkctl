package cli

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	version "github.com/hashicorp/go-version"
	"github.com/spf13/cobra"
)

const (
	roksbnkctlRepo    = "jgruberf5/roksbnkctl"
	selfUpdateTimeout = 5 * time.Minute
)

var (
	flagUpgradeVersion string
	flagUpgradeYes     bool
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade (or pin) the roksbnkctl binary to a GitHub release",
	Long: `Downloads a roksbnkctl release for this OS/arch from GitHub, verifies its
SHA256 against the release's checksums.txt, and replaces the running binary
in place. With no --version it upgrades to the latest release; --version pins
a specific release (and may downgrade or reinstall).

Works on Linux, macOS, and Windows. On Windows the running .exe cannot be
overwritten, so it is moved aside to <binary>.old and the new binary takes its
place; the .old file is removed automatically on the next run.

Requires write permission on the binary's directory (a /usr/local/bin install
needs sudo; Homebrew/Scoop installs should use their own upgrade verb).

Note: release binaries are not yet code-signed, so on a host with an
application-allowlist policy (e.g. Windows Device Guard/WDAC) the freshly
downloaded binary may be blocked until its hash is trusted.`,
	Args: cobra.NoArgs,
	RunE: runUpgrade,
}

func init() {
	upgradeCmd.Flags().StringVar(&flagUpgradeVersion, "version", "", "release to install, e.g. v1.20.1 (default: latest)")
	upgradeCmd.Flags().BoolVarP(&flagUpgradeYes, "yes", "y", false, "skip the confirmation prompt")
	rootCmd.AddCommand(upgradeCmd)
}

// runUpgrade backs `roksbnkctl upgrade [--version vX.Y.Z] [--yes]`. With no
// --version on an interactive terminal it lists the releases newer than the
// running binary and lets the operator pick one; --version or --yes keep the
// non-interactive behaviour (pin, or latest).
func runUpgrade(cmd *cobra.Command, _ []string) error {
	ctx, cancel := context.WithTimeout(cmdContext(cmd), selfUpdateTimeout)
	defer cancel()
	pinned := strings.TrimSpace(flagUpgradeVersion)
	if pinned == "" && !flagUpgradeYes && isTTY() {
		picked, err := pickNewerRelease(ctx, os.Stderr)
		if err != nil {
			return err
		}
		if picked == "" {
			fmt.Fprintln(os.Stderr, "Nothing to install (already up to date, or cancelled).")
			return nil
		}
		pinned = picked
	}
	return selfUpdate(ctx, os.Stderr, pinned, flagUpgradeYes)
}

// pickNewerRelease lists the releases strictly newer than the running Version
// (all of them when the running Version is a non-release build like "dev") and
// prompts the operator to choose. Returns the chosen tag, or "" when there is
// nothing newer or the operator cancels.
func pickNewerRelease(ctx context.Context, w io.Writer) (string, error) {
	rels, err := fetchReleases(ctx)
	if err != nil {
		return "", err
	}
	var cur *version.Version
	if v, verr := version.NewVersion(strings.TrimPrefix(Version, "v")); verr == nil {
		cur = v
	}
	var newer []string
	for _, r := range rels {
		if r.TagName == "" {
			continue
		}
		v, verr := version.NewVersion(strings.TrimPrefix(r.TagName, "v"))
		if verr != nil {
			continue
		}
		if cur == nil || v.GreaterThan(cur) {
			newer = append(newer, r.TagName)
		}
	}
	if len(newer) == 0 {
		return "", nil
	}
	fmt.Fprintf(w, "Current version: %s\n\nNewer releases available:\n", Version)
	for i, t := range newer {
		suffix := ""
		if i == 0 {
			suffix = "  (latest)"
		}
		fmt.Fprintf(w, "  %d) %s%s\n", i+1, t, suffix)
	}
	choice := promptInt("Pick a release to install (0 to cancel)", 1)
	if choice <= 0 || choice > len(newer) {
		return "", nil
	}
	return newer[choice-1], nil
}

// runSelfUpdate backs the legacy `roksbnkctl self update` — always latest,
// always interactive. Kept for compatibility; `upgrade` is the primary verb.
func runSelfUpdate(cmd *cobra.Command, _ []string) error {
	ctx, cancel := context.WithTimeout(cmdContext(cmd), selfUpdateTimeout)
	defer cancel()
	return selfUpdate(ctx, os.Stderr, "", false)
}

// selfUpdate is the shared pipeline behind `upgrade` and `self update`:
//
//  1. resolve the target release (latest, or the pinned --version tag)
//  2. find the asset matching this OS/arch (goreleaser naming)
//  3. download the archive + checksums.txt
//  4. verify SHA256
//  5. extract the roksbnkctl binary (tar.gz on unix, zip on windows)
//  6. install it over the running binary (atomic rename on unix; move-aside
//     on windows, which cannot overwrite a running .exe)
//
// pinned is empty for "latest"; when non-empty it names a specific release
// (a leading `v` is optional) and permits a downgrade or reinstall. auto
// skips the confirmation prompt.
func selfUpdate(ctx context.Context, w io.Writer, pinned string, auto bool) error {
	pinned = strings.TrimSpace(pinned)

	var (
		rel *ghRelease
		err error
	)
	if pinned == "" {
		fmt.Fprintln(w, "→ Checking for the latest release")
		rel, err = fetchLatestRelease(ctx)
	} else {
		tag := normalizeTag(pinned)
		fmt.Fprintf(w, "→ Fetching release %s\n", tag)
		rel, err = fetchReleaseByTag(ctx, tag)
	}
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "  Current: %s\n", Version)
	fmt.Fprintf(w, "  Target:  %s\n", rel.TagName)

	switch {
	case pinned == "" && sameVersion(Version, rel.TagName):
		fmt.Fprintln(w, "✓ Already at the latest release")
		return nil
	case sameVersion(Version, rel.TagName):
		fmt.Fprintf(w, "  (reinstalling %s)\n", rel.TagName)
	case Version == "dev":
		fmt.Fprintln(w, "  (current is a dev build)")
	}

	if !auto && !promptYesNo(fmt.Sprintf("Switch to %s?", rel.TagName), true) {
		return errors.New("aborted")
	}

	aName := assetName(rel.TagName)
	asset, ok := findAsset(rel.Assets, aName)
	if !ok {
		return fmt.Errorf("no asset matching %q in release %s — no artefact for %s/%s",
			aName, rel.TagName, runtime.GOOS, runtime.GOARCH)
	}
	sums, ok := findAsset(rel.Assets, "checksums.txt")
	if !ok {
		return errors.New("no checksums.txt in release; refusing to update without checksum verification")
	}

	fmt.Fprintf(w, "→ Downloading %s (%d bytes)\n", asset.Name, asset.Size)
	archive, err := httpGetBytes(ctx, asset.URL)
	if err != nil {
		return err
	}

	fmt.Fprintln(w, "→ Verifying checksum")
	expected, err := checksumFor(ctx, sums.URL, asset.Name)
	if err != nil {
		return err
	}
	if actual := sha256Hex(archive); actual != expected {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", asset.Name, expected, actual)
	}

	fmt.Fprintln(w, "→ Extracting binary")
	bin, err := extractBnkctlBinary(archive)
	if err != nil {
		return err
	}

	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving running binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}

	fmt.Fprintf(w, "→ Replacing %s\n", self)
	if err := replaceBinary(self, bin); err != nil {
		return fmt.Errorf("replacing binary: %w", err)
	}
	fmt.Fprintf(w, "✓ Now at %s\n", rel.TagName)
	if runtime.GOOS == "windows" {
		fmt.Fprintf(w, "  (previous binary moved to %s.old — removed on the next run)\n", self)
	}
	return nil
}

// normalizeTag ensures a leading `v` so both "1.20.1" and "v1.20.1" resolve to
// the release tag the GitHub tags API expects. Empty stays empty.
func normalizeTag(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

// sameVersion compares two version strings ignoring a leading `v`. goreleaser
// stamps Version without the `v` (e.g. "1.20.1") while GitHub tag names carry
// it (e.g. "v1.20.1"), so a naive `==` never matches a real release.
func sameVersion(a, b string) bool {
	return strings.TrimPrefix(a, "v") == strings.TrimPrefix(b, "v")
}

// ── github metadata ─────────────────────────────────────────────────

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Assets  []ghAsset `json:"assets"`
}

func fetchLatestRelease(ctx context.Context) (*ghRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", roksbnkctlRepo)
	return getRelease(ctx, url, fmt.Sprintf("no releases for %s yet", roksbnkctlRepo))
}

func fetchReleases(ctx context.Context) ([]ghRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=50", roksbnkctlRepo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "roksbnkctl")
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github returned %s listing releases", resp.Status)
	}
	var rels []ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rels); err != nil {
		return nil, err
	}
	return rels, nil
}

// fetchReleaseByTag resolves a specific release (the --version pin). The tag
// must be exact (e.g. v1.20.1); GitHub's tags endpoint is not fuzzy.
func fetchReleaseByTag(ctx context.Context, tag string) (*ghRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/%s", roksbnkctlRepo, tag)
	return getRelease(ctx, url, fmt.Sprintf("release %s not found for %s", tag, roksbnkctlRepo))
}

// getRelease GETs a GitHub release JSON endpoint and decodes it. notFoundMsg
// is surfaced verbatim on a 404 so latest-vs-tag callers give a tailored error.
func getRelease(ctx context.Context, url, notFoundMsg string) (*ghRelease, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "roksbnkctl")
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, errors.New(notFoundMsg)
	default:
		return nil, fmt.Errorf("github returned %s", resp.Status)
	}
	var r ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

// assetName matches goreleaser's default name_template:
// "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}{ext}".
// .Version is the tag without leading 'v'.
func assetName(tag string) string {
	ver := strings.TrimPrefix(tag, "v")
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("roksbnkctl_%s_%s_%s%s", ver, runtime.GOOS, runtime.GOARCH, ext)
}

func findAsset(assets []ghAsset, name string) (ghAsset, bool) {
	for _, a := range assets {
		if a.Name == name {
			return a, true
		}
	}
	return ghAsset{}, false
}

// ── network + crypto helpers ────────────────────────────────────────

func httpGetBytes(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "roksbnkctl")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// checksumFor parses a goreleaser-style checksums.txt and returns the
// hex SHA256 for filename. Format: "<sha256>  <name>".
func checksumFor(ctx context.Context, url, filename string) (string, error) {
	body, err := httpGetBytes(ctx, url)
	if err != nil {
		return "", err
	}
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		parts := strings.Fields(sc.Text())
		if len(parts) >= 2 && parts[1] == filename {
			return parts[0], nil
		}
	}
	return "", fmt.Errorf("checksum not found for %s in checksums.txt", filename)
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// ── extraction + atomic replace ─────────────────────────────────────

// extractBnkctlBinary pulls the roksbnkctl binary out of a goreleaser archive
// (which holds the binary + LICENSE + README.md at the top level). Unix
// releases ship a .tar.gz containing `roksbnkctl`; Windows ships a .zip
// containing `roksbnkctl.exe`.
func extractBnkctlBinary(archive []byte) ([]byte, error) {
	if runtime.GOOS == "windows" {
		return extractFromZip(archive, "roksbnkctl.exe")
	}
	return extractFromTarGz(archive, "roksbnkctl")
}

func extractFromTarGz(archive []byte, wantBase string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar reader: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if filepath.Base(hdr.Name) != wantBase {
			continue
		}
		return io.ReadAll(tr)
	}
	return nil, fmt.Errorf("%s not found in tarball", wantBase)
}

func extractFromZip(archive []byte, wantBase string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("zip reader: %w", err)
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || filepath.Base(f.Name) != wantBase {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("opening %s in zip: %w", f.Name, err)
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}
	return nil, fmt.Errorf("%s not found in zip", wantBase)
}

// replaceBinary stages newBinary as a temp file in the same dir as target
// (same-dir is critical: rename is atomic only on the same filesystem, and
// brew/scoop/manual-install layouts have target on the system partition while
// /tmp is sometimes on tmpfs), then installs it over target via the
// platform-appropriate strategy.
func replaceBinary(target string, newBinary []byte) error {
	dir := filepath.Dir(target)
	base := filepath.Base(target)

	tmp, err := os.CreateTemp(dir, "."+base+".tmp.*")
	if err != nil {
		return fmt.Errorf("creating temp file in %s: %w (write permission?)", dir, err)
	}
	staged := tmp.Name()
	installed := false
	defer func() {
		if !installed {
			_ = os.Remove(staged)
		}
	}()

	if _, err := tmp.Write(newBinary); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if err := installBinary(target, staged); err != nil {
		return err
	}
	installed = true
	return nil
}

// installBinary swaps the staged binary in for target. On unix the running
// binary is an unlinked-but-open inode, so an atomic rename over it just works.
// Windows locks the running image and refuses an overwrite, so we use the
// move-aside dance instead.
func installBinary(target, staged string) error {
	if runtime.GOOS == "windows" {
		return installByMoveAside(target, staged)
	}
	if err := os.Rename(staged, target); err != nil {
		return fmt.Errorf("renaming onto %s: %w (try with sudo, or use brew/scoop upgrade)", target, err)
	}
	return nil
}

// renameFile indirects os.Rename so the unrecoverable branch below can be
// driven in a test. Tests that stub it must not run in parallel — nothing in
// internal/cli calls t.Parallel() today, and adding it to a test that touches
// this var would race. Ordinary filesystem permissions cannot reach it: renaming
// needs write on the containing directory, and target and target.old share one,
// so any permission change that fails the rollback also fails the first rename
// and the function returns before the branch runs. Rather than leave the one
// path that strands a user uncovered, the syscall gets a seam.
var renameFile = os.Rename

// installByMoveAside implements the Windows-safe replace: a running .exe cannot
// be overwritten, but it CAN be renamed (the process's handle follows the
// rename). So move target to target.old, then rename the staged binary into
// target's place. The .old file stays locked until the old process exits;
// sweepStaleBinary removes it on the next run.
//
// On failure the move is rolled back. If the rollback ITSELF fails there is no
// binary at target and the only copy is the sidecar, so the error says so and
// names the file — see errRollbackFailed. That is the one outcome this function
// cannot prevent, and the difference between a one-command recovery and a user
// who thinks the tool deleted itself.
func installByMoveAside(target, staged string) error {
	old := target + ".old"
	_ = os.Remove(old) // clear a stale sidecar from a prior upgrade
	if err := renameFile(target, old); err != nil {
		return fmt.Errorf("moving current binary aside: %w", err)
	}
	if err := renameFile(staged, target); err != nil {
		if rerr := renameFile(old, target); rerr != nil {
			return errRollbackFailed{install: err, rollback: rerr, old: old, target: target}
		}
		return fmt.Errorf("installing new binary: %w", err)
	}
	_ = os.Remove(old) // best-effort now (locked while running); swept next start
	return nil
}

// errRollbackFailed reports the one state installByMoveAside cannot recover
// from: the new binary would not move into place AND the old one would not move
// back, leaving nothing at target.
//
// It exists as a type rather than a fmt.Errorf so both underlying errors stay
// available to errors.Is/As — a caller checking for fs.ErrPermission should see
// it through either cause — and so the recovery path can be asserted in a test
// without matching on prose.
type errRollbackFailed struct {
	install  error // why the new binary could not be installed
	rollback error // why the old binary could not be put back
	old      string
	target   string
}

func (e errRollbackFailed) Error() string {
	return fmt.Sprintf(
		"installing new binary: %v; rolling back also failed: %v\n"+
			"There is now NO binary at %s. Your previous one is intact at %s — "+
			"rename it back to recover:\n    %s",
		e.install, e.rollback, e.target, e.old, recoverCommand(runtime.GOOS, e.old, e.target))
}

// recoverCommand returns the shell command that puts the old binary back.
//
// installBinary only calls installByMoveAside when GOOS is windows, so in
// practice every reader of this message is on Windows — where `mv` is not a
// command. cmd.exe has no such builtin and ships no mv.exe; PowerShell aliases
// it to Move-Item, so Unix advice happens to work in one of the two shells a
// Windows user might be in and fails silently in the other, at the moment they
// have no working binary. printPATHGuidance in install.go exists because this
// same class of bug shipped once already.
//
// The paths are interpolated with %s inside literal quotes rather than %q: %q
// applies Go escaping, which doubles every backslash and produces a command
// naming a path that does not exist.
// goos is a parameter rather than a read of runtime.GOOS so both branches are
// testable anywhere: CI builds for Windows but runs tests only on ubuntu and
// macos, so a runtime.GOOS check would leave the branch that actually ships to
// users permanently unexercised.
func recoverCommand(goos, old, target string) string {
	if goos == "windows" {
		return fmt.Sprintf("Move-Item -LiteralPath \"%s\" -Destination \"%s\"", old, target)
	}
	return fmt.Sprintf("mv \"%s\" \"%s\"", old, target)
}

// Unwrap exposes both causes so errors.Is/As match either one.
func (e errRollbackFailed) Unwrap() []error { return []error{e.install, e.rollback} }

// sweepStaleBinary removes a <self>.old left by a prior Windows upgrade, once
// the old process is gone (so the file is unlocked). Best-effort and a no-op
// off Windows and when no sidecar exists. Called once at startup.
func sweepStaleBinary() {
	if runtime.GOOS != "windows" {
		return
	}
	self, err := os.Executable()
	if err != nil {
		return
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	_ = os.Remove(self + ".old")
}
