// Command tfvars-md parses terraform/variables.tf (plus each submodule's
// own variables.tf) and emits a single Markdown reference document on
// stdout covering every Terraform variable with type, default,
// description, sensitive flag, and source file.
//
// Usage:
//
//	go run ./tools/refgen/tfvars-md > book/src/29-terraform-variable-reference.md
//
// Re-run on every terraform/variables.tf change. The output is checked
// into the book/ directory; the generator itself isn't part of the
// release binary.
//
// Why regex instead of hashicorp/hcl?
//
// roksbnkctl's go.mod doesn't depend on hashicorp/hcl, and the
// terraform/variables.tf shape is regular enough that a small regex
// parser handles the realistic surface (every `variable "<name>" { … }`
// block declares some subset of `type`, `default`, `description`,
// `sensitive`). Adding hashicorp/hcl as a direct dep just for this
// one-page generator would inflate the dep graph by ~40 transitive
// packages. The regex parser is ~200 LOC; the tradeoff is acceptable
// for a build-only tool.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Variable is a single parsed terraform variable block.
type Variable struct {
	Name        string
	Type        string
	Default     string // verbatim default literal; "" means no default declared (i.e. required input)
	Description string
	Sensitive   bool
	Source      string // relative path of the variables.tf the block lives in
}

// Module is one terraform submodule's worth of parsed variables.
type Module struct {
	Name string
	Vars []Variable
}

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "tfvars-md: %v\n", err)
		os.Exit(1)
	}
}

// run discovers terraform/variables.tf + terraform/modules/<m>/variables.tf,
// parses each, and renders the aggregated chapter to w.
//
// Each module gets its own H2 section; within a section, variables are
// listed in source order (so users reading the chapter top-to-bottom
// see the same flow as anyone reading variables.tf directly).
func run(w io.Writer) error {
	rootFile := "terraform/variables.tf"
	moduleGlob := "terraform/modules/*/variables.tf"

	rootVars, err := parseFile(rootFile)
	if err != nil {
		return fmt.Errorf("root: %w", err)
	}

	moduleFiles, err := filepath.Glob(moduleGlob)
	if err != nil {
		return fmt.Errorf("globbing modules: %w", err)
	}
	sort.Strings(moduleFiles)

	var modules []Module
	for _, mf := range moduleFiles {
		vars, err := parseFile(mf)
		if err != nil {
			return fmt.Errorf("%s: %w", mf, err)
		}
		// Module name is the parent directory of variables.tf.
		moduleName := filepath.Base(filepath.Dir(mf))
		modules = append(modules, Module{Name: moduleName, Vars: vars})
	}

	return renderMarkdown(w, rootVars, modules)
}

// variableBlockRe matches the opening line of a variable block.
// terraform's HCL is regular enough that the `{` is always at the
// same line as the variable header. The body is consumed greedily up
// to the matching `}` at column 0 (brace-balance counter in the
// scanner).
var variableBlockRe = regexp.MustCompile(`(?m)^variable\s+"([^"]+)"\s*\{`)

// fieldRe matches a single key = value line inside a variable block.
// parseFile reads a terraform variables.tf file and returns its
// parsed blocks. Returns an empty slice (not nil) when the file has
// no variables — keeps downstream rendering branchless.
func parseFile(path string) ([]Variable, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	src := string(data)
	relPath := path // path is already repo-relative in our invocation
	// Strip leading "./" if present.
	relPath = strings.TrimPrefix(relPath, "./")

	var out []Variable
	// Find every `variable "<name>" {` opener; for each, scan forward
	// until the matching brace closes (depth 0).
	matches := variableBlockRe.FindAllStringSubmatchIndex(src, -1)
	for _, m := range matches {
		name := src[m[2]:m[3]]
		// Body starts right after the `{`.
		bodyStart := m[1] // includes the `{`
		// Find matching close brace.
		bodyEnd := findMatchingBrace(src, bodyStart-1)
		if bodyEnd < 0 {
			return nil, fmt.Errorf("unbalanced braces for variable %q in %s", name, path)
		}
		body := src[bodyStart:bodyEnd]

		v := Variable{
			Name:   name,
			Source: relPath,
		}
		v.Type = extractField(body, "type")
		v.Default = extractField(body, "default")
		v.Description = unquoteIfQuoted(extractField(body, "description"))
		v.Sensitive = strings.EqualFold(extractField(body, "sensitive"), "true")
		out = append(out, v)
	}
	return out, nil
}

// findMatchingBrace returns the index of the `}` that closes the `{`
// at openIdx, ignoring braces inside strings. Returns -1 if no match.
func findMatchingBrace(src string, openIdx int) int {
	if openIdx >= len(src) || src[openIdx] != '{' {
		return -1
	}
	depth := 0
	inString := false
	escape := false
	for i := openIdx; i < len(src); i++ {
		c := src[i]
		if escape {
			escape = false
			continue
		}
		if c == '\\' && inString {
			escape = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch c {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// extractField walks `body` looking for a top-level `<name> = …` line
// (i.e. at brace depth 1, the body's own depth). Returns the raw
// right-hand side, trimmed. For multi-line values (list/object
// literals), captures the full delimited region. Returns "" when the
// field isn't present.
func extractField(body, name string) string {
	lines := strings.Split(body, "\n")
	prefix := name + " "
	prefixEq := name + "="
	for i, ln := range lines {
		trim := strings.TrimSpace(ln)
		// Skip nested blocks; only depth-1 attributes here.
		if !strings.HasPrefix(trim, prefix) && !strings.HasPrefix(trim, prefixEq) {
			continue
		}
		idx := strings.Index(trim, "=")
		if idx < 0 {
			continue
		}
		value := strings.TrimSpace(trim[idx+1:])
		// Handle multi-line list/object literals: if the value starts
		// with `[` or `{` and isn't closed on the same line, walk
		// forward concatenating until balanced.
		if (strings.HasPrefix(value, "[") || strings.HasPrefix(value, "{")) && !lineBalanced(value) {
			joined := value
			for j := i + 1; j < len(lines); j++ {
				joined += " " + strings.TrimSpace(lines[j])
				if lineBalanced(joined) {
					break
				}
			}
			value = joined
		}
		return value
	}
	return ""
}

// lineBalanced reports whether parens / brackets / braces in s are
// balanced (ignoring contents of double-quoted strings). Used to
// decide whether a multi-line value literal has terminated.
func lineBalanced(s string) bool {
	depthBrk, depthBrc, depthPar := 0, 0, 0
	inString := false
	escape := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escape {
			escape = false
			continue
		}
		if c == '\\' && inString {
			escape = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch c {
		case '[':
			depthBrk++
		case ']':
			depthBrk--
		case '{':
			depthBrc++
		case '}':
			depthBrc--
		case '(':
			depthPar++
		case ')':
			depthPar--
		}
	}
	return depthBrk == 0 && depthBrc == 0 && depthPar == 0
}

// unquoteIfQuoted strips surrounding `"…"` from s if present. Used for
// description values where the raw HCL retains the quotes.
func unquoteIfQuoted(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// renderMarkdown emits the aggregated chapter.
func renderMarkdown(w io.Writer, rootVars []Variable, modules []Module) error {
	fmt.Fprintln(w, "# Terraform variable reference")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Auto-generated by `go run ./tools/refgen/tfvars-md > book/src/29-terraform-variable-reference.md`. Re-run on every `terraform/variables.tf` change.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Every variable below is settable via `terraform.tfvars`, `-var`, `-var-file`, or (for sensitive values) the corresponding `TF_VAR_<name>` environment variable. Variables with `_required_` defaults must be set explicitly. See [Chapter 13](./13-terraform-variables.md) for how roksbnkctl threads these through the workspace config.")
	fmt.Fprintln(w)

	fmt.Fprintln(w, "## Root module variables")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Source: `%s`\n\n", "terraform/variables.tf")
	writeVariableTable(w, rootVars)
	fmt.Fprintln(w)

	for _, m := range modules {
		fmt.Fprintf(w, "## Module: `%s`\n\n", m.Name)
		fmt.Fprintf(w, "Source: `terraform/modules/%s/variables.tf`\n\n", m.Name)
		writeVariableTable(w, m.Vars)
		fmt.Fprintln(w)
	}
	return nil
}

// writeVariableTable emits a markdown table for one module's variables.
// Each row: `| name | type | default | description | sensitive |`.
func writeVariableTable(w io.Writer, vars []Variable) {
	if len(vars) == 0 {
		fmt.Fprintln(w, "_(no variables declared)_")
		return
	}
	fmt.Fprintln(w, "| Variable | Type | Default | Description | Sensitive |")
	fmt.Fprintln(w, "|---|---|---|---|---|")
	for _, v := range vars {
		def := v.Default
		if def == "" {
			def = "_required_"
		} else {
			def = "`" + def + "`"
		}
		typ := v.Type
		if typ == "" {
			typ = "—"
		} else {
			typ = "`" + typ + "`"
		}
		sensitive := "no"
		if v.Sensitive {
			sensitive = "**yes**"
		}
		desc := escapePipes(v.Description)
		if desc == "" {
			desc = "—"
		}
		fmt.Fprintf(w, "| `%s` | %s | %s | %s | %s |\n", v.Name, typ, def, desc, sensitive)
	}
}

// escapePipes makes a string safe to drop into a Markdown table cell
// by escaping any literal `|` characters (which would otherwise
// terminate the cell early) and collapsing newlines to spaces.
func escapePipes(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "|", `\|`)
	return s
}
