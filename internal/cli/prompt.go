package cli

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"
)

// stdinReader is shared across prompts so buffered bytes from one prompt
// don't get dropped before the next.
var stdinReader = bufio.NewReader(os.Stdin)

// isTTY reports whether stdin is a terminal. Non-TTY runs use defaults
// for every prompt instead of blocking on input — important for CI.
func isTTY() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// promptString reads a single line. Empty input returns def. Non-TTY
// stdin returns def without reading.
func promptString(label, def string) string {
	if !isTTY() {
		return def
	}
	if def != "" {
		fmt.Fprintf(os.Stderr, "  %-30s [%s]: ", label, def)
	} else {
		fmt.Fprintf(os.Stderr, "  %-30s: ", label)
	}
	s, _ := stdinReader.ReadString('\n')
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	return s
}

// promptInt parses the line as an integer. Bad input falls back to def
// (after a one-line warning) — keeps `roksbnkctl init` from aborting on a
// fat-finger.
func promptInt(label string, def int) int {
	s := promptString(label, strconv.Itoa(def))
	n, err := strconv.Atoi(s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  (not a number, using %d)\n", def)
		return def
	}
	return n
}

// promptSelect prints a numbered menu and returns the chosen 0-based index.
// Empty input or a non-TTY stdin returns def (the default option). def is
// clamped into range. Mirrors promptInt's "bad input → re-ask, never abort"
// behaviour for a fat-finger.
func promptSelect(label string, options []string, def int) int {
	if def < 0 || def >= len(options) {
		def = 0
	}
	if !isTTY() || len(options) == 0 {
		return def
	}
	fmt.Fprintf(os.Stderr, "  %s:\n", label)
	for i, o := range options {
		marker := " "
		if i == def {
			marker = "*"
		}
		fmt.Fprintf(os.Stderr, "    %s %2d) %s\n", marker, i+1, o)
	}
	for attempt := 0; attempt < 100; attempt++ {
		s := promptString(fmt.Sprintf("Choice [1-%d]", len(options)), strconv.Itoa(def+1))
		n, err := strconv.Atoi(s)
		if err == nil && n >= 1 && n <= len(options) {
			return n - 1
		}
		fmt.Fprintf(os.Stderr, "  (enter a number 1-%d)\n", len(options))
	}
	return def
}

// promptYesNo accepts y/n (any case, prefix match). Empty returns def.
// Non-TTY returns def. Default is shown with capitalisation: [Y/n] or [y/N].
func promptYesNo(label string, def bool) bool {
	if !isTTY() {
		return def
	}
	suffix := "[y/N]"
	if def {
		suffix = "[Y/n]"
	}
	fmt.Fprintf(os.Stderr, "  %-30s %s: ", label, suffix)
	s, _ := stdinReader.ReadString('\n')
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return def
	}
	return strings.HasPrefix(s, "y")
}
