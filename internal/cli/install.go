package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

var (
	flagInstallDir   string
	flagInstallForce bool
)

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Copy the running roksbnkctl binary into a directory on PATH",
	Long: `Install the roksbnkctl binary you're currently running into a directory
on $PATH so you can invoke it as ` + "`roksbnkctl`" + ` from any working
directory.

Default destination:
  Linux/macOS, in order of preference:
    $HOME/.local/bin  (preferred — typically writable without sudo)
    $HOME/bin         (older convention; still on PATH for some setups)
    /usr/local/bin    (system-wide; usually needs sudo)
  Windows:
    a writable directory already on %PATH% — preferring
    %LOCALAPPDATA%\Microsoft\WindowsApps (on the per-user PATH by default,
    no admin) — so the binary resolves immediately. Falls back to
    %LOCALAPPDATA%\Programs\roksbnkctl (with a PATH hint) if none is usable.

Override the destination with --dir.

Idempotent: if the running binary already lives at the destination,
prints a message and exits 0. Use --force to overwrite (useful right
after a local rebuild that landed at the install path).

Examples:
  roksbnkctl install                       # default — ~/.local/bin
  roksbnkctl install --dir ~/bin           # specific user dir
  sudo roksbnkctl install --dir /usr/local/bin   # system-wide

Note: this is distinct from ` + "`roksbnkctl self update`" + `, which
pulls the latest GitHub release tarball over the network.`,
	Args: cobra.NoArgs,
	RunE: runInstall,
}

func init() {
	installCmd.Flags().StringVar(&flagInstallDir, "dir", "", "destination directory (default: a PATH dir — ~/.local/bin on Unix, %LOCALAPPDATA%\\Microsoft\\WindowsApps on Windows)")
	installCmd.Flags().BoolVar(&flagInstallForce, "force", false, "overwrite even if destination resolves to the running binary")
	rootCmd.AddCommand(installCmd)
}

func runInstall(_ *cobra.Command, _ []string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving running binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}

	binName := "roksbnkctl"
	if runtime.GOOS == "windows" {
		binName = "roksbnkctl.exe"
	}

	destDir := flagInstallDir
	if destDir == "" {
		destDir = chooseInstallDir()
	}
	// Expand a leading ~ since shells don't expand it inside a quoted
	// flag value the way they do for a positional bareword.
	if home, err := os.UserHomeDir(); err == nil {
		if destDir == "~" {
			destDir = home
		} else if strings.HasPrefix(destDir, "~/") {
			destDir = filepath.Join(home, destDir[2:])
		}
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w (try --dir DIR or sudo)", destDir, err)
	}

	dest := filepath.Join(destDir, binName)

	// Idempotent: running binary IS the destination → no-op.
	if !flagInstallForce {
		if absSelf, err := filepath.Abs(self); err == nil {
			if absDest, err := filepath.Abs(dest); err == nil && absSelf == absDest {
				fmt.Fprintf(os.Stderr, "✓ Already installed at %s\n", dest)
				return nil
			}
		}
	}

	fmt.Fprintf(os.Stderr, "→ Copying %s → %s\n", self, dest)
	if err := copyExecutable(self, dest); err != nil {
		return fmt.Errorf("copying: %w (write permission? try --dir or sudo)", err)
	}
	fmt.Fprintf(os.Stderr, "✓ Installed %s\n", dest)

	if !isOnPATH(destDir) {
		printPATHGuidance(os.Stderr, destDir)
	} else if runtime.GOOS == "windows" {
		fmt.Fprintln(os.Stderr, "  (open a new terminal if `roksbnkctl` doesn't resolve immediately)")
	} else {
		fmt.Fprintln(os.Stderr, "  (open a new shell or run `hash -r` if `roksbnkctl` doesn't resolve immediately)")
	}
	return nil
}

// printPATHGuidance prints OS-appropriate instructions for putting dir on PATH.
// On Windows the Unix `export PATH` / rc-file advice is meaningless (and was what
// a Windows install used to emit), so give the PowerShell user-PATH one-liner.
func printPATHGuidance(w io.Writer, dir string) {
	fmt.Fprintf(w, "\nwarning: %s is not on your PATH\n", dir)
	if runtime.GOOS == "windows" {
		fmt.Fprintln(w, "  add it to your user PATH (new terminals will pick it up):")
		fmt.Fprintf(w, "    [Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path','User') + ';%s', 'User')\n", dir)
		fmt.Fprintln(w, "  then open a new terminal.")
		return
	}
	fmt.Fprintln(w, "  add this to your shell's rc file (~/.bashrc / ~/.zshrc / etc.):")
	fmt.Fprintf(w, "    export PATH=\"%s:$PATH\"\n", dir)
	fmt.Fprintln(w, "  then `hash -r` or open a new shell.")
}

// chooseInstallDir picks a sensible default destination, preferring
// paths that don't need sudo. ~/.local/bin is the modern convention
// (XDG-ish, on PATH by default in most distros' login profiles).
func chooseInstallDir() string {
	if runtime.GOOS == "windows" {
		return chooseInstallDirWindows()
	}
	if home, err := os.UserHomeDir(); err == nil {
		local := filepath.Join(home, ".local", "bin")
		if isOnPATH(local) || dirExists(local) {
			return local
		}
		homeBin := filepath.Join(home, "bin")
		if isOnPATH(homeBin) || dirExists(homeBin) {
			return homeBin
		}
		// Neither exists yet — create ~/.local/bin and warn about PATH
		// after the install. Better than failing on /usr/local/bin's
		// permission check for a user who has no sudo.
		return local
	}
	return "/usr/local/bin"
}

// chooseInstallDirWindows picks a destination that is ALREADY on the user's
// %PATH% so `roksbnkctl` resolves immediately — Windows has no ~/.local/bin
// convention, and defaulting there (the old cross-platform behaviour) installed
// the binary somewhere PATH never looks. Order:
//  1. %LOCALAPPDATA%\Microsoft\WindowsApps — on the per-user PATH by default on
//     Windows 10/11, user-writable (no admin), so this is the common answer.
//  2. the first writable directory on %PATH% under the user's profile (avoids
//     admin-only system dirs like System32).
//  3. a per-user dir we create; runInstall then prints the PATH hint.
func chooseInstallDirWindows() string {
	home, _ := os.UserHomeDir()
	local := os.Getenv("LOCALAPPDATA")

	if local != "" {
		if winApps := filepath.Join(local, "Microsoft", "WindowsApps"); isOnPATH(winApps) && dirWritable(winApps) {
			return winApps
		}
	}
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if p == "" {
			continue
		}
		if isUnderDir(p, home) && dirWritable(p) {
			return p
		}
	}
	if local != "" {
		return filepath.Join(local, "Programs", "roksbnkctl")
	}
	if home != "" {
		return filepath.Join(home, ".roksbnkctl", "bin")
	}
	return "."
}

// isOnPATH reports whether dir is in $PATH (after Abs-ing both sides
// so trailing slashes / relative entries match). Case-insensitive on Windows,
// where PATH entries commonly differ in case from the resolved dir.
func isOnPATH(dir string) bool {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if p == "" {
			continue
		}
		if pAbs, err := filepath.Abs(p); err == nil && pathEqual(pAbs, abs) {
			return true
		}
	}
	return false
}

// pathEqual compares two absolute paths, case-insensitively on Windows.
func pathEqual(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// isUnderDir reports whether path is dir or a descendant of it (used to keep the
// Windows PATH scan to user-scoped, admin-free directories).
func isUnderDir(path, dir string) bool {
	if dir == "" {
		return false
	}
	absP, err1 := filepath.Abs(path)
	absD, err2 := filepath.Abs(dir)
	if err1 != nil || err2 != nil {
		return false
	}
	if pathEqual(absP, absD) {
		return true
	}
	prefix := absD + string(os.PathSeparator)
	if runtime.GOOS == "windows" {
		return strings.HasPrefix(strings.ToLower(absP), strings.ToLower(prefix))
	}
	return strings.HasPrefix(absP, prefix)
}

func dirExists(d string) bool {
	info, err := os.Stat(d)
	return err == nil && info.IsDir()
}

// dirWritable reports whether an EXISTING directory can be written to, by
// creating and removing a probe file (the reliable cross-platform check — Windows
// ACLs make a stat-based guess unreliable). Never creates the directory itself.
func dirWritable(dir string) bool {
	if !dirExists(dir) {
		return false
	}
	f, err := os.CreateTemp(dir, ".roksbnkctl-wtest-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// copyExecutable writes src to dest atomically: temp file in dest's
// dir, chmod 0755, then rename onto dest. Same-dir is required for
// rename to be atomic on most filesystems.
func copyExecutable(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dest), "."+filepath.Base(dest)+".tmp.*")
	if err != nil {
		return fmt.Errorf("creating temp file in %s: %w", filepath.Dir(dest), err)
	}
	defer os.Remove(tmp.Name())

	if _, err := io.Copy(tmp, in); err != nil {
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
	return os.Rename(tmp.Name(), dest)
}
