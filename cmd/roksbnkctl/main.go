// Command roksbnkctl is the user-facing CLI entrypoint.
//
// All command logic lives in internal/cli; this file just hands off so the
// cli package stays importable for tests. A small argv preflight runs
// BEFORE rootCmd.Execute() to reject the stuck-together short-flag-value
// form (e.g. `-ws canada-roks`) that cobra/pflag would otherwise silently
// parse as `-w s` + a silently-dropped positional. argvPreflight below
// documents the rejection rule and the error it produces.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/jgruberf5/roksbnkctl/internal/cli"
	"github.com/jgruberf5/roksbnkctl/internal/exitcode"
)

func main() {
	root := cli.RootCommand()
	if err := argvPreflight(root, os.Args[1:], os.Stderr); err != nil {
		// Preflight already wrote the actionable error to stderr; just
		// exit non-zero. cli.Execute() must NOT run — the typo must
		// surface before any RunE / workspace mutation / IAM call.
		//
		// Usage, not Failure: nothing was attempted, so a script can tell a
		// malformed invocation from a command that ran and went wrong.
		os.Exit(exitcode.Usage)
	}
	cli.Execute()
}

// argvPreflight scans argv for the stuck-together short-flag-value form
// (`-X<suffix>` where X is a value-requiring short flag, <suffix> is
// non-empty, and <suffix>[0] is not `=`). On detection it writes an
// actionable error to w (naming the offending token, both acceptable
// forms `-X value` / `-X=value`, the long-flag equivalents, and a "did
// you mean" derived from the binary's actual flag set) and returns a
// non-nil error so main() can exit non-zero before Execute() runs.
//
// The collected short-flag set is derived from the live cobra tree
// (root + every subcommand, persistent + local flags) — no hand-
// maintained typo list. Only short flags with NoOptDefVal == ""
// (value-requiring; not booleans like -v/-q) are in the rejection set,
// so legitimate boolean short flags pass through unaffected.
//
// argv tokens that fall under a DisableFlagParsing subcommand (e.g.
// `roksbnkctl kubectl -nfoo get pods` — the `-nfoo` is meant for
// kubectl, not roksbnkctl) are skipped: we resolve the matched command
// via rootCmd.Find and scan only the prefix that precedes its name.
// argv tokens after a `--` literal are also skipped (positional sentinel).
//
// w is the stderr sink (passed in for testability).
func argvPreflight(root *cobra.Command, argv []string, w io.Writer) error {
	// 1. Collect value-requiring shorts: shorthand byte → long name.
	shorts := collectValueRequiringShorts(root)
	if len(shorts) == 0 {
		return nil
	}

	// 2. Determine the cutoff: if the matched cobra command has
	//    DisableFlagParsing, only scan argv tokens BEFORE that command's
	//    name. Otherwise scan all argv tokens (up to a `--` sentinel).
	cutoff := len(argv)
	if matched, _, err := root.Find(argv); err == nil && matched != nil && matched != root && matched.DisableFlagParsing {
		if idx := findCommandNameIndex(argv, matched); idx >= 0 {
			cutoff = idx
		}
	}

	// 3. Scan each in-scope token for the rejected shape.
	for i := 0; i < cutoff; i++ {
		tok := argv[i]
		if tok == "--" {
			return nil // positional sentinel — everything after is non-flag
		}
		// Must start with exactly one '-' and at least 3 chars (`-X<suffix>`
		// with <suffix> non-empty), not `--longflag` (two leading dashes).
		if len(tok) < 3 || tok[0] != '-' || tok[1] == '-' {
			continue
		}
		short := tok[1]
		longName, isValue := shorts[short]
		if !isValue {
			continue
		}
		// `-X=value` is the ACCEPTED equals form — pass through.
		if tok[2] == '=' {
			continue
		}
		// `-X<suffix>` with <suffix> non-empty and not starting with `=`
		// is the rejected stuck-together form.
		suffix := tok[2:]
		writePreflightError(w, tok, string(short), longName, suffix)
		return fmt.Errorf("argv preflight: stuck-together short-flag-value %q", tok)
	}
	return nil
}

// collectValueRequiringShorts walks the cobra command tree (root + every
// subcommand transitively) and returns the union of value-requiring
// short flags. A flag is "value-requiring" when its NoOptDefVal is
// empty — pflag uses NoOptDefVal == "" to distinguish strings/ints/etc.
// from booleans (which set NoOptDefVal to "true"). Returned map keys
// are the single-byte shorthand; values are the long-name used in the
// "did you mean" suggestion.
//
// Persistent flags are visited via VisitAll on each cobra.Command's
// PersistentFlags() and Flags() in turn — VisitAll on Flags() already
// includes inherited persistent flags after cobra has merged them, but
// since the merge happens lazily we walk both buckets explicitly to be
// robust against pre-Execute() calls.
func collectValueRequiringShorts(root *cobra.Command) map[byte]string {
	out := map[byte]string{}
	addFlag := func(f *pflag.Flag) {
		if f.Shorthand == "" || len(f.Shorthand) != 1 {
			return
		}
		if f.NoOptDefVal != "" {
			// boolean-style or "optional value" flag — pflag accepts
			// `-X` alone, so stuck-together-value is unambiguous as
			// long-name lookup wouldn't help anyway.
			return
		}
		b := f.Shorthand[0]
		// First writer wins so the long-name suggestion is stable across
		// rebuilds; collisions on the same short across subcommands are
		// non-fatal — the union-set's purpose is just "is this byte a
		// known value-requiring short anywhere in the tree?".
		if _, exists := out[b]; !exists {
			out[b] = f.Name
		}
	}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		c.PersistentFlags().VisitAll(addFlag)
		c.Flags().VisitAll(addFlag)
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	return out
}

// findCommandNameIndex returns the index in argv at which cmd.Name() (or
// one of cmd.Aliases) first appears. Used to bound the preflight scan
// when the matched command has DisableFlagParsing — only argv tokens
// before the command name are roksbnkctl's argv to police; tokens at-
// or-after belong to the wrapped tool.
func findCommandNameIndex(argv []string, cmd *cobra.Command) int {
	names := append([]string{cmd.Name()}, cmd.Aliases...)
	for i, tok := range argv {
		for _, n := range names {
			if tok == n {
				return i
			}
		}
	}
	return -1
}

// writePreflightError emits the actionable error per Sprint 21 README
// decision (5). It names BOTH the literal offending token AND both
// acceptable shapes (`-X value` / `-X=value`), the long-flag equivalents,
// AND a "did you mean" derived from the binary's actual flag set so the
// operator can copy/paste the fix.
func writePreflightError(w io.Writer, tok, short, long, suffix string) {
	fmt.Fprintf(w, "roksbnkctl: %q is not a recognised flag (looks like a stuck-together short-flag-value, which this binary does not accept).\n", tok)
	fmt.Fprintln(w, "Use one of these forms instead:")
	fmt.Fprintf(w, "  -%s %s              (short, space)\n", short, suffix)
	fmt.Fprintf(w, "  -%s=%s              (short, equals)\n", short, suffix)
	fmt.Fprintf(w, "  --%s %s     (long, space)\n", long, suffix)
	fmt.Fprintf(w, "  --%s=%s     (long, equals)\n", long, suffix)
	fmt.Fprintf(w, "Did you mean '--%s %s'?\n", long, suffix)
}
