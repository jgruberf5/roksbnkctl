package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// citedRepoPath matches a repo-relative path named inside a comment:
// internal/…, terraform/…, scripts/…, book/…, cmd/…, tools/…, docs/… with a
// file extension.
//
// The extension must be a real, LOWERCASE file extension. "any 1-6 characters
// after a dot" is not enough: Go package selectors have exactly that shape, so
// internal/naming.Derive and internal/remote.Client are read as files that do not
// exist. The first version of this scan reported ten missing files of which six
// were selectors, and the second still reported five after its own comment warned
// about precisely this.
var citedRepoPath = regexp.MustCompile(
	`(?:^|[\s(\x60"'` + "`" + `])((?:internal|terraform|scripts|book|cmd|tools|docs)/[A-Za-z0-9_./-]+\.(?:go|sh|md|tf|tfvars|ya?ml|json|html|py|toml|txt|example))\b`)

// Comments must not cite files that do not exist.
//
// A comment naming a path is a promise the reader will follow, and following it
// to nothing costs more than the comment saved. Four such paths were found in
// this repository when this was written, cited from nine places: a test helper
// file removed with `init --var-file`, an Artifactory deploy script removed with
// the FLP feature, and two gated-live e2e drivers removed at v1.12.0. Two had
// been wrong for dozens of releases.
//
// The four are named in the PR that added this test rather than here, because
// naming them in a comment would make this file trip its own check — and the
// alternative, excluding this file, would leave a carve-out that could later hide
// a real citation. That is the same shape as a gitleaks allowlist entry whose
// explanation names the banned string.
//
// This is the same defect as `BuildClientset` documenting a
// default path it does not use (#277): documentation describing something no code
// implements, which reads as authoritative precisely because it is specific.
//
// Deliberately narrow. It only checks paths that LOOK like this repository's own
// files, and only inside comments. A comment naming a third-party path, a URL, or
// a file the user is expected to create is not this test's business.
func TestCommentsDoNotCiteFilesThatDoNotExist(t *testing.T) {
	root := repoRootForDocTest(t)

	var findings []string
	for _, dir := range []string{"internal", "cmd", "tools"} {
		walkGoFiles(t, filepath.Join(root, dir), func(path string, lines []string) {
			rel, _ := filepath.Rel(root, path)
			for i, line := range lines {
				s := strings.TrimSpace(line)
				if !strings.HasPrefix(s, "//") {
					continue
				}
				for _, m := range citedRepoPath.FindAllStringSubmatch(s, -1) {
					cited := m[1]
					if strings.Contains(cited, "*") {
						continue // a glob, not a path
					}
					if _, err := os.Stat(filepath.Join(root, cited)); err == nil {
						continue
					}
					findings = append(findings,
						rel+":"+itoa(i+1)+" cites "+cited+", which does not exist")
				}
			}
		})
	}

	if len(findings) > 0 {
		t.Errorf("%d comment(s) cite a file that is not in the repository.\n"+
			"A path in a comment is a promise the reader follows; following it to nothing "+
			"costs more than the comment saved. Update it to where the thing lives now, or "+
			"say plainly that it was removed:\n  %s",
			len(findings), strings.Join(findings, "\n  "))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
