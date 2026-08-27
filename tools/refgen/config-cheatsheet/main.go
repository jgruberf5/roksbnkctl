// Command config-cheatsheet renders every possible config.yaml entry as a
// single self-contained HTML page.
//
// WHY GENERATED. A hand-written cheatsheet is a second copy of the schema, and a
// second copy is the defect this codebase keeps finding: `bnk.flp.vsi.reach`
// (#210) had a config field, an override, a book entry and a tfvars render with
// nothing reading it, and chapter 27 had silently drifted because it was
// generated and then hand-edited. This walks the same `Workspace` struct the
// loader uses, so a field that exists here exists there, and
// TestConfigCheatsheetIsCurrent fails the build when the two diverge.
//
// WHAT IT ADDS OVER CHAPTER 28. That chapter lists fields per STRUCT, which only
// helps if you already know the nesting. This resolves the full dotted path an
// operator actually types — `bnk.flp.vsi.subnet_cidr`, not `SubnetCIDR` on
// `FLPVSICfg` — and pairs each one with its ROKSBNKCTL_* override, taken from
// the derived mapping in internal/config rather than a parallel table.
//
//	go run ./tools/refgen/config-cheatsheet > scripts/demos/config-cheatsheet.html
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"html"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

const srcFile = "internal/config/workspace.go"

// changelogFile is where the version stamp comes from.
//
// NOT ldflags, and not a build timestamp. The checked-in file is compared
// byte-for-byte by TestConfigCheatsheetIsCurrent, so anything that varies
// between two runs of the same commit would make the test fail on Tuesday and
// pass on Wednesday. The CHANGELOG's newest released heading is in the repo,
// changes exactly once per release, and is already the source
// TestDemoRunnerTagMatchesTheCurrentRelease uses — so the release commit that
// rolls the heading up regenerates this file in the same breath.
const changelogFile = "CHANGELOG.md"

// forgeEnvFile holds the BNK_FORGE_* constants. Parsed rather than retyped: a
// cheatsheet that lists an environment variable the code does not read is worse
// than one that omits it.
const forgeEnvFile = "internal/cli/bnkforge.go"

// secret is a setting that deliberately never appears in config.yaml.
//
// THE PAGE IS WRONG WITHOUT THESE. It answers "every field config.yaml accepts",
// which is true and is not the question an operator has. Asked how to set the
// BNK Forge password, a fields-only page says nothing -- and the correct answer
// is that there is no such field ON PURPOSE: the password is never written to
// the file, it comes from the environment or a prompt, and the session token
// goes to the OS keychain. Omitting that reads as "unsupported" rather than
// "deliberately elsewhere".
type secret struct {
	Env  string
	Sets string
	How  string
}

// maxDepth bounds the walk. The schema nests about five deep; the bound exists
// so a self-referential type cannot spin forever rather than to limit real
// nesting, and exceeding it is reported rather than silently truncated.
const maxDepth = 12

type field struct {
	Key      string // the yaml key at this level
	GoType   string // as written in the struct
	Line     string // BNK line: 2.3, 2.4, or both
	Default  string
	Doc      string
	TypeName string // the named struct type to recurse into, if any
}

type structDoc struct {
	Name   string
	Doc    string
	Fields []field
}

// entry is one row of the cheatsheet: a fully-resolved leaf.
type entry struct {
	Path    string // dotted yaml path, e.g. bnk.flp.vsi.subnet_cidr
	Section string // top-level key, for grouping
	GoType  string
	Line    string
	Default string
	Doc     string
	Env     string // ROKSBNKCTL_* override, or "" when none reaches it
	Req     bool
}

func main() {
	structs, err := parseStructs(srcFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	root, ok := structs["Workspace"]
	if !ok {
		fmt.Fprintf(os.Stderr, "no Workspace struct in %s — the schema root moved\n", srcFile)
		os.Exit(1)
	}

	// path -> env, inverted from the DERIVED override mapping. Two overrides can
	// legitimately reach one path (an alias, or one per list index); join them so
	// none is hidden.
	//
	// INDEXES ARE COLLAPSED TO `[]`. The per-zone overrides report
	// bnk.network.zones[1].ext_vlan_cidr and so on, one per zone. Trimming only a
	// trailing "[0]" left all eighteen of them off the page — and multizone is
	// the ONLY shape this tool deploys, so the fields an operator most needs were
	// the ones missing. Collapsing every index to `[]` puts the whole family on
	// one row with its three variables listed.
	envFor := map[string][]string{}
	for name, path := range config.OverridePaths() {
		if path == "" {
			continue
		}
		envFor[collapseIndexes(path)] = append(envFor[collapseIndexes(path)], name)
	}
	for k := range envFor {
		sort.Strings(envFor[k])
	}

	// Requiredness comes from config.RequiredConfigFields, NOT from `omitempty`.
	// omitempty is a marshalling directive and says nothing about whether a value
	// must be supplied; deriving from it marked 25 fields required when four are,
	// and missed `prefix` (#229 review).
	required := map[string]bool{}
	for _, f := range config.RequiredConfigFields {
		required[f] = true
	}

	var entries []entry
	walk(structs, root, "", "", 0, envFor, required, &entries)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })

	fmt.Print(render(entries, structs, currentVersion(), outsideConfig()))
}

// walk resolves every leaf under sd, following named struct types.
func walk(all map[string]*structDoc, sd *structDoc, prefix, section string, depth int, envFor map[string][]string, required map[string]bool, out *[]entry) {
	if depth > maxDepth {
		fmt.Fprintf(os.Stderr, "warning: %s exceeds depth %d — the schema nests deeper than expected, "+
			"and the cheatsheet is truncated there rather than looping\n", prefix, maxDepth)
		return
	}
	for _, f := range sd.Fields {
		path := f.Key
		if prefix != "" {
			path = prefix + "." + f.Key
		}
		sec := section
		if sec == "" {
			sec = f.Key
		}
		child, nested := all[f.TypeName]
		// A MAP of structs is a leaf: the operator invents the keys, so
		// enumerating the element's fields under a dotted path would name paths
		// that do not exist.
		if nested && strings.HasPrefix(f.GoType, "map[") {
			nested = false
		}
		// A SLICE of structs is not. bnk.network.zones is a list whose element
		// fields are exactly what an operator fills in, and leaving them as one
		// opaque row hides the six fields multizone actually needs. They are
		// emitted as `zones[].ext_vlan_cidr`, which is what the shape is.
		if nested && strings.HasPrefix(f.GoType, "[]") {
			*out = append(*out, entry{
				Path: path, Section: sec, GoType: f.GoType, Line: f.Line,
				Default: f.Default, Doc: f.Doc,
				Env: strings.Join(envAt(envFor, path), " / "), Req: required[path],
			})
			walk(all, child, path+"[]", sec, depth+1, envFor, required, out)
			continue
		}
		if nested {
			walk(all, child, path, sec, depth+1, envFor, required, out)
			continue
		}
		*out = append(*out, entry{
			Path:    path,
			Section: sec,
			GoType:  f.GoType,
			Line:    f.Line,
			Default: f.Default,
			Doc:     f.Doc,
			Env:     strings.Join(envAt(envFor, path), " / "),
			Req:     required[path],
		})
	}
}

func parseStructs(src string) (map[string]*structDoc, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, src, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", src, err)
	}
	structs := map[string]*structDoc{}
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
				key := strings.Split(y, ",")[0]
				if key == "" {
					continue
				}
				line := tag.Get("line")
				if line == "" {
					line = "both"
				}
				gt := typeString(fl.Type)
				sd.Fields = append(sd.Fields, field{
					Key:      key,
					GoType:   gt,
					Line:     line,
					Default:  tag.Get("default"),
					Doc:      firstSentence(docText(fl.Doc, fl.Comment)),
					TypeName: baseTypeName(gt),
				})
			}
			structs[sd.Name] = sd
		}
	}
	return structs, nil
}

// envAt returns the overrides that reach a field, looking under both the plain
// path and the indexed one.
//
// A list of SCALARS is a leaf whose path carries no suffix -- cluster.http_allowed_cidrs
// -- while the override machinery reports where it writes, which is
// cluster.http_allowed_cidrs[0]. Looking only at the plain path dropped ten
// CIDR-list variables off the page; looking only at the indexed one would drop
// every scalar. Both, and the set is complete.
func envAt(envFor map[string][]string, path string) []string {
	if v := envFor[path]; len(v) > 0 {
		return v
	}
	return envFor[path+"[]"]
}

// collapseIndexes rewrites a[1].b into a[].b so every element of a list shares
// one row, however many indexes the override machinery reports.
func collapseIndexes(path string) string {
	return regexp.MustCompile(`\[\d+\]`).ReplaceAllString(path, "[]")
}

// baseTypeName strips pointer/slice/map decoration to the underlying name, so
// *FLPVSICfg and []COSUpload both resolve to a struct this can look up.
func baseTypeName(gt string) string {
	gt = strings.TrimPrefix(gt, "*")
	gt = strings.TrimPrefix(gt, "[]")
	if i := strings.Index(gt, "]"); strings.HasPrefix(gt, "map[") && i >= 0 {
		gt = gt[i+1:]
	}
	gt = strings.TrimPrefix(gt, "*")
	return gt
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

func docText(groups ...*ast.CommentGroup) string {
	var b strings.Builder
	for _, g := range groups {
		if g == nil {
			continue
		}
		for _, c := range g.List {
			s := strings.TrimPrefix(c.Text, "//")
			s = strings.TrimPrefix(s, "/*")
			s = strings.TrimSuffix(s, "*/")
			b.WriteString(strings.TrimSpace(s))
			b.WriteString(" ")
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func endsSentence(s string, i int) bool {
	if i+1 < len(s) && s[i+1] != ' ' {
		return false
	}
	for _, a := range []string{"e.g.", "i.e.", "etc.", "vs.", "cf."} {
		if i+1 >= len(a) && strings.EqualFold(s[i+1-len(a):i+1], a) {
			return false
		}
	}
	return true
}

// firstSentence keeps the row readable. The full prose lives in chapter 28 and
// in the struct itself; a table cell carrying six sentences is a table nobody
// scans.
func firstSentence(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '.' && endsSentence(s, i) {
			return strings.TrimSpace(s[:i+1])
		}
	}
	return strings.TrimSpace(s)
}

func esc(s string) string { return html.EscapeString(s) }

// currentVersion reads the newest released heading from the CHANGELOG.
//
// An unreadable CHANGELOG or a missing heading yields "dev" rather than an
// error: a cheatsheet with no version on it is still a usable cheatsheet, and
// failing the generator would fail the build for a cosmetic string.
func currentVersion() string {
	b, err := os.ReadFile(changelogFile)
	if err != nil {
		return "dev"
	}
	m := regexp.MustCompile(`(?m)^## (v\d+\.\d+\.\d+)`).FindStringSubmatch(string(b))
	if m == nil {
		return "dev"
	}
	return m[1]
}

// outsideConfig lists the settings that are supplied outside config.yaml.
//
// The BNK_FORGE_* names are read from the source constants so they cannot drift
// from what the code actually looks up; the descriptions are the reasons the
// struct comments already give.
func outsideConfig() []secret {
	names := map[string]string{}
	if b, err := os.ReadFile(forgeEnvFile); err == nil {
		re := regexp.MustCompile(`(?m)^\s*(envForge\w+)\s*=\s*"([A-Z_]+)"`)
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			names[m[1]] = m[2]
		}
	}
	out := []secret{{
		Env:  "IBMCLOUD_API_KEY",
		Sets: "The IBM Cloud API key every phase authenticates with.",
		How:  "Exported in the environment, or resolved per ibmcloud.api_key_source (env | keychain | config | prompt). Also settable as ibmcloud.api_key_b64, which stores it in the workspace.",
	}}
	if v, ok := names["envForgePassword"]; ok {
		out = append(out, secret{
			Env:  v,
			Sets: "The BNK Forge login password.",
			How:  "Exported in the environment, passed as --password, stored as bnkforge.password_b64, or typed at the prompt — in that precedence. The resulting session token is cached in the OS keychain, so the password is normally used once. scripts/generate_b64_password.sh produces the stored form.",
		})
	}
	if v, ok := names["envForgeUser"]; ok {
		out = append(out, secret{
			Env:  v,
			Sets: "The BNK Forge login user.",
			How:  "Overrides bnkforge.username, which IS a config.yaml field. Also settable as --username.",
		})
	}
	if v, ok := names["envForgeURL"]; ok {
		out = append(out, secret{
			Env:  v,
			Sets: "The BNK Forge server URL.",
			How:  "Overrides bnkforge.url, which IS a config.yaml field. Also settable as --url.",
		})
	}
	return out
}

func render(entries []entry, structs map[string]*structDoc, version string, secrets []secret) string {
	var b strings.Builder
	withEnv := 0
	for _, e := range entries {
		if e.Env != "" {
			withEnv++
		}
	}

	b.WriteString(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>roksbnkctl config.yaml cheatsheet</title>
<!-- GENERATED FILE - DO NOT EDIT.
     Produced by: go run ./tools/refgen/config-cheatsheet > scripts/demos/config-cheatsheet.html
     Enforced by: TestConfigCheatsheetIsCurrent in internal/config.
     Edit the struct doc comments in internal/config/workspace.go instead. -->
<style>
  :root { color-scheme: light; }
  * { box-sizing: border-box; }
  body {
    background: #ffffff;
    color: #1a1a1a;
    font: 15px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
    margin: 0;
    padding: 2rem 1.5rem 4rem;
  }
  .wrap { max-width: 1400px; margin: 0 auto; }
  h1 { font-size: 1.6rem; margin: 0 0 .35rem; }
  .ver { font-size: .78rem; font-weight: 600; vertical-align: middle; background: #1c66d1; color: #fff; border-radius: 999px; padding: .18rem .6rem; margin-left: .4rem; letter-spacing: .02em; }
  .sub { color: #555; margin: 0 0 1.25rem; }
  .sub code { background: #f2f5f8; padding: 1px 5px; border-radius: 3px; }
  .bar { display: flex; gap: .75rem; align-items: center; flex-wrap: wrap; margin: 0 0 1rem; position: sticky; top: 0; background: #fff; padding: .75rem 0; border-bottom: 1px solid #e3e8ee; z-index: 2; }
  #q { flex: 1 1 320px; min-width: 220px; padding: .5rem .7rem; font-size: .95rem; border: 1px solid #c6d0da; border-radius: 5px; }
  #q:focus { outline: 2px solid #1c66d1; outline-offset: -1px; }
  .count { color: #555; font-size: .85rem; white-space: nowrap; }
  .chip { font-size: .78rem; border: 1px solid #c6d0da; background: #fff; border-radius: 999px; padding: .28rem .7rem; cursor: pointer; color: #333; }
  .chip[aria-pressed="true"] { background: #1c66d1; border-color: #1c66d1; color: #fff; }
  table { border-collapse: collapse; width: 100%; font-size: .88rem; }
  th, td { border: 1px solid #dbe2ea; padding: .45rem .6rem; text-align: left; vertical-align: top; }
  th { background: #f5f8fb; position: sticky; top: 62px; font-weight: 600; z-index: 1; }
  tbody tr:nth-child(even) { background: #fbfcfd; }
  tbody tr:hover { background: #f0f6ff; }
  code { font-family: "SF Mono", Menlo, Consolas, monospace; font-size: .86em; }
  td.path code { color: #99235c; font-weight: 600; }
  td.env code { color: #0b6b52; }
  td.type code { color: #555; }
  .req { color: #b60205; font-weight: 600; }
  .opt { color: #888; }
  .sec { background: #eef3f9 !important; font-weight: 600; }
  .sec td { border-top: 2px solid #c6d0da; }
  .muted { color: #999; }
  .note { border-left: 3px solid #1c66d1; background: #f6f9fd; padding: .7rem .9rem; margin: 1.25rem 0; font-size: .9rem; }
  footer { margin-top: 2rem; color: #777; font-size: .82rem; border-top: 1px solid #e3e8ee; padding-top: .9rem; }
  @media print { .bar { position: static; } th { position: static; } body { padding: 0; } }
</style>
</head>
<body>
<div class="wrap">
`)

	fmt.Fprintf(&b, `<h1>roksbnkctl <code>config.yaml</code> cheatsheet <span class="ver">%s</span></h1>
<p class="sub">Every field the workspace schema accepts, with the dotted path you actually type
and the <code>ROKSBNKCTL_*</code> override that sets it. Generated from
<code>internal/config/workspace.go</code> — %d fields, %d reachable by an environment override.</p>
`, esc(version), len(entries), withEnv)

	b.WriteString(`<div class="bar">
  <input id="q" type="search" placeholder="Filter by path, env var, type or description…" autocomplete="off">
  <button class="chip" id="f-req" aria-pressed="false">Required only</button>
  <button class="chip" id="f-env" aria-pressed="false">Has env override</button>
  <button class="chip" id="f-24" aria-pressed="false">BNK 2.4</button>
  <span class="count" id="count"></span>
</div>

<div class="note">
<strong>Two ways to set the same thing.</strong> A field can come from
<code>config.yaml</code> or from its <code>ROKSBNKCTL_*</code> variable; the
environment wins. <code>roksbnkctl config yaml</code> and
<code>roksbnkctl config env</code> print either form, and
<code>--from-yaml</code> / <code>--from-env</code> convert between them.
</div>

<table>
<thead><tr>
  <th style="width:26%">Path</th>
  <th style="width:12%">Type</th>
  <th style="width:6%">Req</th>
  <th style="width:8%">Line</th>
  <th style="width:11%">Default</th>
  <th style="width:22%">Env override</th>
  <th>Description</th>
</tr></thead>
<tbody>
`)

	section := ""
	for _, e := range entries {
		if e.Section != section {
			section = e.Section
			sd := ""
			if s, ok := structs["Workspace"]; ok {
				for _, f := range s.Fields {
					if f.Key == section {
						sd = f.Doc
					}
				}
			}
			fmt.Fprintf(&b, `<tr class="sec" data-sec="1"><td colspan="7"><code>%s</code> &nbsp;<span class="muted">%s</span></td></tr>`+"\n",
				esc(section), esc(sd))
		}
		req := `<span class="opt">—</span>`
		if e.Req {
			req = `<span class="req">yes</span>`
		}
		def := `<span class="muted">—</span>`
		if e.Default != "" {
			def = "<code>" + esc(e.Default) + "</code>"
		}
		env := `<span class="muted">—</span>`
		if e.Env != "" {
			env = "<code>" + esc(e.Env) + "</code>"
		}
		fmt.Fprintf(&b, `<tr data-req="%t" data-env="%t" data-line="%s">`+
			`<td class="path"><code>%s</code></td>`+
			`<td class="type"><code>%s</code></td>`+
			`<td>%s</td><td>%s</td><td>%s</td>`+
			`<td class="env">%s</td><td>%s</td></tr>`+"\n",
			e.Req, e.Env != "", esc(e.Line),
			esc(e.Path), esc(e.GoType), req, esc(e.Line), def, env, esc(e.Doc))
	}

	b.WriteString(`</tbody>
</table>

<h2 id="outside">Set outside <code>config.yaml</code></h2>
<p class="sub">Supplied from the environment rather than the file. Where a
<code>_b64</code> field also exists, the environment wins — so a runner can override a
stored value without editing the workspace. A credential written into
<code>config.yaml</code> is a credential in your backups and, if you are careless, your
git remote: <code>scripts/generate_b64_password.sh</code> keeps it out of your shell
history at least.</p>
<table>
<thead><tr>
  <th style="width:22%">Environment variable</th>
  <th style="width:26%">Sets</th>
  <th>How to supply it</th>
</tr></thead>
<tbody>
`)
	for _, sc := range secrets {
		fmt.Fprintf(&b, `<tr><td class="env"><code>%s</code></td><td>%s</td><td>%s</td></tr>`+"\n",
			esc(sc.Env), esc(sc.Sets), esc(sc.How))
	}
	b.WriteString(`</tbody>
</table>

<footer>
Generated from <code>internal/config/workspace.go</code> by
<code>go run ./tools/refgen/config-cheatsheet</code>. Do not edit by hand — a
currency test regenerates and compares it, so an edit here is reverted and a new
field appears here without anyone adding it.
</footer>
</div>

<script>
(function () {
  var q = document.getElementById('q');
  var count = document.getElementById('count');
  var rows = Array.prototype.slice.call(document.querySelectorAll('tbody tr'));
  var data = rows.filter(function (r) { return !r.dataset.sec; });
  var filters = { req: false, env: false, l24: false };

  function apply() {
    var needle = q.value.toLowerCase().trim();
    var shown = 0;
    data.forEach(function (r) {
      var ok = true;
      if (needle && r.textContent.toLowerCase().indexOf(needle) === -1) ok = false;
      if (ok && filters.req && r.dataset.req !== 'true') ok = false;
      if (ok && filters.env && r.dataset.env !== 'true') ok = false;
      // "both" applies to 2.4 as well — a field is hidden only when it is 2.3-only.
      if (ok && filters.l24 && r.dataset.line === '2.3') ok = false;
      r.style.display = ok ? '' : 'none';
      if (ok) shown++;
    });
    // A section header with nothing under it is noise.
    rows.filter(function (r) { return r.dataset.sec; }).forEach(function (h) {
      var n = h.nextElementSibling, any = false;
      while (n && !n.dataset.sec) {
        if (n.style.display !== 'none') { any = true; break; }
        n = n.nextElementSibling;
      }
      h.style.display = any ? '' : 'none';
    });
    count.textContent = shown + ' of ' + data.length + ' fields';
  }

  function toggle(id, key) {
    var el = document.getElementById(id);
    el.addEventListener('click', function () {
      filters[key] = !filters[key];
      el.setAttribute('aria-pressed', String(filters[key]));
      apply();
    });
  }
  q.addEventListener('input', apply);
  toggle('f-req', 'req');
  toggle('f-env', 'env');
  toggle('f-24', 'l24');
  apply();
})();
</script>
</body>
</html>
`)
	return b.String()
}
