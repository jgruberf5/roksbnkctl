package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	pathFlag := flag.String("path", "", "Path to the repo (default: detect via git)")
	onceFlag := flag.Bool("once", false, "Render once to stdout and exit (no TTY needed)")
	flag.Parse()

	root, err := resolveRoot(*pathFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sprintwatch:", err)
		os.Exit(1)
	}
	if _, err := os.Stat(filepath.Join(root, "issues")); err != nil {
		fmt.Fprintf(os.Stderr, "sprintwatch: no issues/ directory under %s\n", root)
		os.Exit(1)
	}

	if *onceFlag {
		m := InitialModel(root)
		m.width = 100
		msg := loadCmd(root)().(loadedMsg)
		if msg.err != nil {
			fmt.Fprintln(os.Stderr, msg.err)
			os.Exit(1)
		}
		m.sprints = msg.sprints
		m.burndowns = msg.burndowns
		m.lastRefresh = time.Now()
		fmt.Println(m.View())
		return
	}

	p := tea.NewProgram(InitialModel(root), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func resolveRoot(flagVal string) (string, error) {
	if flagVal != "" {
		return filepath.Abs(flagVal)
	}
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("not inside a git repository — pass --path /path/to/repo")
	}
	return strings.TrimSpace(string(out)), nil
}
