// Command env-example generates the annotated .env template `roksbnkctl config
// env` prints.
//
// It is generated rather than written because the alternative is a hand-kept
// list of 124 variable names beside the code that reads them, which is the drift
// this repo keeps paying for: a .env naming a variable that no longer exists
// looks exactly like one naming a variable that does.
//
// Two sources, each authoritative for its half:
//
//   - config.OverridePaths() for name -> config path. Derived by probing the
//     override machinery, so a new override appears here without anyone adding
//     it.
//   - the doc comment on the config field, parsed from workspace.go, for the
//     description. The same text the book's configuration reference uses.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

func main() {
	// Resolve the source from the MODULE ROOT, not the working directory. This
	// runs both from the repo root (a manual `go run`) and from internal/cli (the
	// go:generate directive and the staleness test), and a relative path silently
	// works in one and fails in the other.
	root, err := moduleRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "locating the module root: %v\n", err)
		os.Exit(1)
	}
	docs, err := fieldDocs(filepath.Join(root, "internal", "config", "workspace.go"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading field docs: %v\n", err)
		os.Exit(1)
	}
	paths := config.OverridePaths()

	// Group by the top-level config block so the file reads in the same order as
	// config.yaml rather than alphabetically by variable name.
	groups := map[string][]string{}
	for name, path := range paths {
		g := "other"
		if path != "" && !strings.Contains(path, ",") {
			g = strings.SplitN(path, ".", 2)[0]
		}
		groups[g] = append(groups[g], name)
	}
	var order []string
	for g := range groups {
		order = append(order, g)
	}
	sort.Strings(order)

	var b strings.Builder
	b.WriteString(`# ============================================================================
# roksbnkctl environment overrides.
#
#   roksbnkctl config env > .env                    # this template
#   roksbnkctl config env --from-yaml config.yaml   # the same settings, populated
#   set -a; . ./.env; set +a                        # load it into a shell
#   roksbnkctl -w <ws> init --non-interactive --override-from-env
#
# Every variable here maps to one config.yaml field, shown after the name. A
# workspace can be configured entirely from these, which is what a CI runner
# does -- it has no file to edit.
#
# Values are COMMENTED OUT. Uncomment only what you intend to set: an empty
# assignment is not the same as leaving a setting alone, and unset is what lets
# roksbnkctl apply its default.
# ============================================================================
`)
	for _, g := range order {
		names := groups[g]
		sort.Strings(names)
		fmt.Fprintf(&b, "\n# ---- %s ----\n", g)
		for _, n := range names {
			path := paths[n]
			switch {
			case path == "":
				fmt.Fprintf(&b, "# %s=\n#   (no single config.yaml field -- set with the rest of its group)\n", n)
				continue
			case strings.Contains(path, ","):
				fmt.Fprintf(&b, "# %s=\n#   -> %s\n", n, strings.ReplaceAll(path, ",", ", "))
				continue
			}
			if d := docs[path]; d != "" {
				fmt.Fprintf(&b, "#   %s\n", d)
			}
			fmt.Fprintf(&b, "# %s=          # -> %s\n", n, path)
		}
	}
	fmt.Print(b.String())
}

// fieldDocs maps a dotted config path to the first sentence of its field's doc
// comment, by walking the same struct the config is unmarshalled into.
func fieldDocs(src string) (map[string]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, src, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	// struct name -> yaml key -> doc, plus which struct each block field points at
	byStruct := map[string]map[string]string{}
	blockType := map[string]map[string]string{}

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
			fields := map[string]string{}
			blocks := map[string]string{}
			for _, fl := range st.Fields.List {
				if fl.Tag == nil {
					continue
				}
				y := reflect.StructTag(strings.Trim(fl.Tag.Value, "`")).Get("yaml")
				key := strings.SplitN(y, ",", 2)[0]
				if key == "" || key == "-" {
					continue
				}
				fields[key] = firstSentence(commentText(fl.Doc, fl.Comment))
				if n := namedType(fl.Type); n != "" {
					blocks[key] = n
				}
			}
			byStruct[ts.Name.Name] = fields
			blockType[ts.Name.Name] = blocks
		}
	}

	out := map[string]string{}
	var walk func(structName, prefix string, depth int)
	walk = func(structName, prefix string, depth int) {
		if depth > 4 {
			return
		}
		for key, doc := range byStruct[structName] {
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			if doc != "" {
				out[path] = doc
			}
			if sub, ok := blockType[structName][key]; ok && sub != structName {
				walk(sub, path, depth+1)
			}
		}
	}
	walk("Workspace", "", 0)
	return out, nil
}

func namedType(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return namedType(t.X)
	}
	return ""
}

func commentText(groups ...*ast.CommentGroup) string {
	var parts []string
	for _, g := range groups {
		if g != nil {
			parts = append(parts, strings.TrimSpace(g.Text()))
		}
	}
	return strings.Join(parts, " ")
}

func firstSentence(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	// Go doc convention opens with the field name ("BigIPURL is the management
	// address of..."), which reads as noise beside the variable it documents.
	if i := strings.Index(s, " is "); i > 0 && i < 40 && s[:i] == strings.TrimSpace(s[:i]) && !strings.Contains(s[:i], " ") {
		s = strings.ToUpper(s[i+4:i+5]) + s[i+5:]
	}
	if i := strings.Index(s, ". "); i > 0 {
		s = s[:i+1]
	}
	if len(s) > 150 {
		s = s[:147] + "..."
	}
	return s
}

// moduleRoot walks up from the working directory to the directory holding go.mod.
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod above %s", dir)
		}
		dir = parent
	}
}
