package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

var (
	flagUninstallDir string
	flagUninstallYes bool
)

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove the installed roksbnkctl binary from ~/.local/bin (opposite of install)",
	Long: `Delete the roksbnkctl binary that ` + "`roksbnkctl install`" + ` copied onto $PATH.

By default it removes <install-dir>/roksbnkctl, where <install-dir> is the same
directory ` + "`install`" + ` uses (~/.local/bin, then ~/bin). Override with --dir.

It refuses to delete the binary you are currently running on Windows (a running
.exe cannot be removed there) — delete it manually or run uninstall from a
different binary. On Linux/macOS removing the running (installed) binary is fine.

Examples:
  roksbnkctl uninstall                 # remove ~/.local/bin/roksbnkctl
  roksbnkctl uninstall --dir ~/bin
  sudo roksbnkctl uninstall --dir /usr/local/bin`,
	Args: cobra.NoArgs,
	RunE: runUninstall,
}

func init() {
	uninstallCmd.Flags().StringVar(&flagUninstallDir, "dir", "", "directory to remove roksbnkctl from (default: ~/.local/bin or ~/bin)")
	uninstallCmd.Flags().BoolVarP(&flagUninstallYes, "yes", "y", false, "skip the confirmation prompt")
	rootCmd.AddCommand(uninstallCmd)
}

func runUninstall(_ *cobra.Command, _ []string) error {
	binName := "roksbnkctl"
	if runtime.GOOS == "windows" {
		binName = "roksbnkctl.exe"
	}

	destDir := flagUninstallDir
	if destDir == "" {
		destDir = chooseInstallDir()
	}
	if home, err := os.UserHomeDir(); err == nil {
		if destDir == "~" {
			destDir = home
		} else if strings.HasPrefix(destDir, "~/") {
			destDir = filepath.Join(home, destDir[2:])
		}
	}
	dest := filepath.Join(destDir, binName)

	if _, err := os.Lstat(dest); err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "✓ Nothing to remove — %s is not installed\n", dest)
			return nil
		}
		return fmt.Errorf("checking %s: %w", dest, err)
	}

	// Detect whether the target IS the binary we're running from. On Unix,
	// unlinking a running file is fine. On Windows a running .exe can't be
	// deleted — but it CAN be renamed, so we move it aside to free its path.
	runningSelf := false
	if self, err := os.Executable(); err == nil {
		if resolved, rerr := filepath.EvalSymlinks(self); rerr == nil {
			self = resolved
		}
		absSelf, e1 := filepath.Abs(self)
		absDest, e2 := filepath.Abs(dest)
		if e1 == nil && e2 == nil && absSelf == absDest {
			runningSelf = true
			if runtime.GOOS != "windows" {
				fmt.Fprintf(os.Stderr, "note: removing the currently-running binary (%s)\n", dest)
			}
		}
	}

	if !flagUninstallYes && !promptYesNo(fmt.Sprintf("Remove %s?", dest), true) {
		return errors.New("aborted")
	}

	// Windows: can't delete a running .exe, so move it aside to <name>.old. The
	// install path is freed (effectively uninstalled); the .old remnant unlocks
	// once this process exits and can be deleted any time (a later install/upgrade
	// also sweeps its own .old).
	if runningSelf && runtime.GOOS == "windows" {
		old := dest + ".old"
		_ = os.Remove(old) // clear a stale .old from a prior upgrade (best-effort)
		if err := os.Rename(dest, old); err != nil {
			return fmt.Errorf("cannot delete %s while it is running, and moving it aside failed: %w\n  close roksbnkctl, then delete it manually (PowerShell: Remove-Item %q)", dest, err, dest)
		}
		fmt.Fprintf(os.Stderr, "✓ Removed %s\n  (Windows can't delete a running .exe, so it was moved to %s — unlocked once this process exits; delete it any time)\n", dest, old)
		return nil
	}

	if err := os.Remove(dest); err != nil {
		return fmt.Errorf("removing %s: %w (write permission? try --dir or sudo)", dest, err)
	}
	fmt.Fprintf(os.Stderr, "✓ Removed %s\n", dest)
	return nil
}
