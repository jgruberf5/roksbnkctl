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

	// Guard: removing the currently-running binary. Impossible on Windows;
	// allowed (with a note) on Unix, where unlinking a running file is fine.
	if self, err := os.Executable(); err == nil {
		if resolved, rerr := filepath.EvalSymlinks(self); rerr == nil {
			self = resolved
		}
		absSelf, e1 := filepath.Abs(self)
		absDest, e2 := filepath.Abs(dest)
		if e1 == nil && e2 == nil && absSelf == absDest {
			if runtime.GOOS == "windows" {
				return fmt.Errorf("cannot remove %s while it is the running binary on Windows — delete it manually or run uninstall from a different roksbnkctl", dest)
			}
			fmt.Fprintf(os.Stderr, "note: removing the currently-running binary (%s)\n", dest)
		}
	}

	if !flagUninstallYes && !promptYesNo(fmt.Sprintf("Remove %s?", dest), true) {
		return errors.New("aborted")
	}

	if err := os.Remove(dest); err != nil {
		return fmt.Errorf("removing %s: %w (write permission? try --dir or sudo)", dest, err)
	}
	fmt.Fprintf(os.Stderr, "✓ Removed %s\n", dest)
	return nil
}
