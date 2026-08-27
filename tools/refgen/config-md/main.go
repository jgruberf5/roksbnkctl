// Command config-md parses internal/config/workspace.go and emits the
// workspace `config.yaml` schema reference on stdout.
//
//	go run ./tools/refgen/config-md > book/src/28-configuration-reference.md
//
// WHY THIS EXISTS. Chapter 28 used to be written by hand against a struct with
// 177 fields, and nothing checked that the two agreed. A field could ship
// undocumented, or documented wrongly, and nothing noticed — the same failure
// that let `cneinstance_advanced_env` be documented in three places while no
// code read it. TestConfigReferenceIsCurrent now regenerates this and diffs, so
// the chapter cannot drift from the struct.
//
// The AST is parsed rather than reflected over, because the descriptions ARE
// the doc comments and reflection cannot see them.
//
// LINE APPLICABILITY. Which BNK line a field applies to is not derivable from
// the struct — it lives in terraform's `line_pre_24` gates. So it is carried as
// a struct tag next to the field:
//
//	Foo string `yaml:"foo,omitempty" line:"2.4"`
//
// Absent tag means both lines, which is the safe default: a field wrongly
// marked line-specific hides real configuration, while one wrongly marked
// "both" merely under-promises.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"sort"
	"strings"

	"github.com/jgruberf5/roksbnkctl/tools/refgen/mdesc"
)

type field struct {
	YAMLKey  string
	GoType   string
	Line     string
	Default  string
	Doc      string
	Optional bool
}

type structDoc struct {
	Name   string
	Doc    string
	Fields []field
}

func main() {
	src := "internal/config/workspace.go"
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, src, nil, parser.ParseComments)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse %s: %v\n", src, err)
		os.Exit(1)
	}

	structs := map[string]*structDoc{}
	var order []string

	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			sd := &structDoc{Name: ts.Name.Name, Doc: docText(gd.Doc, ts.Doc)}
			for _, fl := range st.Fields.List {
				if fl.Tag == nil {
					continue
				}
				tag := reflect.StructTag(strings.Trim(fl.Tag.Value, "`"))
				y := tag.Get("yaml")
				if y == "" || y == "-" {
					continue
				}
				parts := strings.Split(y, ",")
				key := parts[0]
				if key == "" {
					continue
				}
				line := tag.Get("line")
				if line == "" {
					line = "both"
				}
				def := tag.Get("default")
				sd.Fields = append(sd.Fields, field{
					YAMLKey:  key,
					GoType:   typeString(fl.Type),
					Line:     line,
					Default:  def,
					Doc:      firstSentence(docText(fl.Doc, fl.Comment)),
					Optional: strings.Contains(y, "omitempty"),
				})
			}
			if len(sd.Fields) > 0 {
				structs[sd.Name] = sd
				order = append(order, sd.Name)
			}
		}
	}

	// Workspace first (it is the document root), then the rest alphabetically —
	// a stable order, so the generated file diffs cleanly.
	sort.Strings(order)
	head := []string{}
	rest := []string{}
	for _, n := range order {
		if n == "Workspace" {
			head = append(head, n)
			continue
		}
		rest = append(rest, n)
	}
	order = append(head, rest...)

	var b strings.Builder
	fmt.Fprint(&b, header)

	for _, n := range order {
		sd := structs[n]
		fmt.Fprintf(&b, "## `%s`\n\n", sd.Name)
		if sd.Doc != "" {
			fmt.Fprintf(&b, "%s\n\n", mdesc.Prepare(sd.Doc))
		}
		fmt.Fprintln(&b, "| key | type | line | default | required | description |")
		fmt.Fprintln(&b, "|---|---|---|---|---|---|")
		for _, fl := range sd.Fields {
			req := "yes"
			if fl.Optional {
				req = "no"
			}
			line := fl.Line
			if line == "both" {
				line = "2.3 + 2.4"
			}
			def := fl.Default
			if def == "" {
				def = "—"
			} else {
				def = "`" + def + "`"
			}
			fmt.Fprintf(&b, "| `%s` | `%s` | %s | %s | %s | %s |\n",
				fl.YAMLKey, fl.GoType, line, def, req, mdesc.Cell(fl.Doc))
		}
		fmt.Fprintln(&b)
	}
	fmt.Print(b.String())
}

func docText(groups ...*ast.CommentGroup) string {
	for _, g := range groups {
		if g == nil {
			continue
		}
		var lines []string
		for _, c := range g.List {
			t := strings.TrimPrefix(c.Text, "//")
			t = strings.TrimPrefix(t, "/*")
			t = strings.TrimSuffix(t, "*/")
			lines = append(lines, strings.TrimSpace(t))
		}
		s := strings.TrimSpace(strings.Join(lines, " "))
		if s != "" {
			return s
		}
	}
	return ""
}

// firstSentence keeps the table one line per field. The full prose stays in the
// source, which the chapter links to — duplicating all of it here would make
// this a second copy to maintain, which is the thing being removed.
// sentenceAbbrevs are the abbreviations whose trailing period does NOT end a
// sentence. Without this, "pins the BNK release, e.g. \"2.4.0-EA\". This is the
// single field that selects the product line" was published as "pins the BNK
// release, e.g." — cut mid-phrase, immediately before the only part that
// mattered. Fourteen rows were truncated this way, and the undocumented-field
// ratchet cannot see it: they are not blank, merely cut off.
var sentenceAbbrevs = map[string]bool{
	"eg": true, "ie": true, "etc": true, "vs": true, "cf": true,
	"approx": true, "inc": true, "no": true, "fig": true, "al": true,
}

// endsSentence reports whether the period at s[i] terminates a sentence, rather
// than sitting inside an abbreviation or a single-letter initial.
func endsSentence(s string, i int) bool {
	start := strings.LastIndexByte(s[:i], ' ') + 1
	// Strip the dots and any leading punctuation: the abbreviation is commonly
	// parenthesised, and "(e.g" would otherwise miss the table entirely.
	word := strings.ToLower(strings.ReplaceAll(s[start:i], ".", ""))
	word = strings.TrimLeft(word, "([{\"'`“‘")
	if word == "" {
		return false
	}
	if sentenceAbbrevs[word] {
		return false
	}
	// A lone letter before the period is an initial ("F. Smith"), not an end.
	return len(word) > 1
}

func firstSentence(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return ""
	}
	for i := 0; i+1 < len(s); i++ {
		if s[i] == '.' && s[i+1] == ' ' && endsSentence(s, i) {
			return s[:i+1]
		}
	}
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

func typeString(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + typeString(t.X)
	case *ast.ArrayType:
		return "[]" + typeString(t.Elt)
	case *ast.MapType:
		return "map[" + typeString(t.Key) + "]" + typeString(t.Value)
	case *ast.SelectorExpr:
		return typeString(t.X) + "." + t.Sel.Name
	case *ast.InterfaceType:
		return "any"
	default:
		return "?"
	}
}

const header = `# Configuration reference

<!-- GENERATED FILE — DO NOT EDIT.
     Produced by: go run ./tools/refgen/config-md > book/src/28-configuration-reference.md
     Enforced by: TestConfigReferenceIsCurrent in internal/config.
     Edit the struct doc comments in internal/config/workspace.go instead. -->

Field-by-field schema for the workspace ` + "`config.yaml`" + `, generated from the
[` + "`Workspace`" + ` struct](https://github.com/jgruberf5/roksbnkctl/blob/main/internal/config/workspace.go)
in ` + "`internal/config/workspace.go`" + `. This chapter is the single source of truth for
what a field is called, what type it takes, and which BNK line it applies to.

For a one-page version carrying the full dotted paths and each field's
` + "`ROKSBNKCTL_*`" + ` override, see the
[**config.yaml cheatsheet**](https://jgruberf5.github.io/roksbnkctl/config-cheatsheet.html)
— generated from the same struct, so the two cannot disagree.

[Chapter 12 — Workspace config](./12-workspace-config.md) is the *teaching* chapter:
use it to learn the shape. Use this one to look up a specific field. Every other
chapter links here rather than restating fields, because a field restated in four
places is a field that will disagree with itself by the next release.

**The ` + "`line`" + ` column** says which BNK release line a field applies to. Most apply to
both. A field marked 2.4 has no effect on a 2.3 install and vice versa — the line
itself is selected by ` + "`bnk.manifest_version`" + ` and nothing else.

**Required** means the field has no ` + "`omitempty`" + `: it is always rendered, and its zero
value is meaningful. It does not mean you must type it into your ` + "`config.yaml`" + `.

**Default** is carried on the field as a ` + "`default:\"...\"`" + ` struct tag. A dash means no
default is declared — either the zero value applies, or the default is computed at
run time and belongs in the prose rather than a table cell.

`
