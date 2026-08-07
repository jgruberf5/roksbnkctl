package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// rejectShiftedFlagValues catches the argument-shift that an EMPTY interpolation
// produces, and reports it as what it is.
//
// Terraform builds these command lines by string interpolation:
//
//	... pull-chart --chart <c> --version ${local.flo_chart_version} --registry-login <h> ...
//
// When that local resolves to "", the rendered line becomes
//
//	... pull-chart --chart <c> --version --registry-login repo.f5.com ...
//
// and the shell hands cobra a `--version` whose value is the NEXT FLAG. Everything
// after it shifts by one, the trailing token falls out as a positional, and the user
// sees `unknown command "repo.f5.com"` — a message about a host, for a bug about a
// missing version. Issue #50 was exactly this, and it cost a full apply cycle to
// diagnose from a message that named nothing relevant.
//
// A flag value beginning with "-" is essentially never intentional here: none of
// these flags take a negative number or a leading-dash literal. So treat it as the
// shift it almost certainly is, name the flag that is empty, and say which one
// swallowed which. Detecting the shift rather than marking flags required keeps
// legitimately-optional flags optional — the bug is the empty interpolation, not the
// absence of a value.
func rejectShiftedFlagValues(cmd *cobra.Command, args []string) error {
	var problems []string
	cmd.Flags().Visit(func(f *pflag.Flag) {
		v := f.Value.String()
		if strings.HasPrefix(v, "-") && len(v) > 1 {
			problems = append(problems, fmt.Sprintf(
				"--%s was given the value %q — that is another flag, so --%s was almost certainly passed with an EMPTY value and every argument after it shifted by one",
				f.Name, v, f.Name))
		}
	})
	if len(problems) == 0 && len(args) == 0 {
		return nil
	}
	var b strings.Builder
	if len(problems) > 0 {
		b.WriteString(strings.Join(problems, "\n  "))
	}
	if len(args) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n  ")
		}
		fmt.Fprintf(&b, "unexpected positional argument(s): %s — this command takes none, so they are the tail of a shifted flag list", strings.Join(args, " "))
	}
	b.WriteString("\n\n  This is what an empty terraform interpolation looks like by the time it reaches\n" +
		"  the CLI. Check the value the caller rendered for each flag above; one of them is\n" +
		"  empty at the source.")
	return fmt.Errorf("%s", b.String())
}
