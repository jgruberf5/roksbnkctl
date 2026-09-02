// Sprint 24 validator Issue 1 — hermetic tests for the
// `roksbnkctl test hosts {list,add,remove,clear}` CLI surface staff
// shipped in `internal/cli/test_hosts.go`. Additive-only — this file
// is new and never edits any pre-existing `_test.go` (Sprint 18
// parity discipline carries forward).
//
// All sub-cases drive cobra in-process via the existing `runRootCmd`
// harness (defined in `internal/cli/roottest_test.go`). A fresh
// `ROKSBNKCTL_HOME=t.TempDir()` per sub-case keeps each run hermetic
// — the persistence path lands in the tempdir's
// `<workspace>/config.yaml` and nothing reaches the operator's real
// `~/.roksbnkctl/` tree.
//
// Sub-case → acceptance-criterion map (validator AC refers to
// the `test hosts` surface review, sub-cases (a)..(l)):
//
//	TestTestHostsAdd_Persists                  → (a)
//	TestTestHostsAdd_Idempotent_AlreadyPresent → (b)
//	TestTestHostsRemove_Persists               → (c)
//	TestTestHostsRemove_Idempotent_Absent      → (d)
//	TestTestHostsList_EmptyZeroBytes           → (e)
//	TestTestHostsList_EmptyJSONArray           → (f)
//	TestTestHostsList_PopulatedOrderStable     → (g)
//	TestTestHostsClear_PromptDefaultsNo        → (h)
//	TestTestHostsClear_AutoSkipsPrompt         → (i)
//	TestTestHostsAdd_NonURL_Errors             → (j)
//	TestTestHostsArgs_NoArgs_ListAndClear      → (k)
//	TestTestHostsArgs_MinimumNArgs_AddAndRemove→ (l)
//
// Discipline:
//   - One new file only; no edits to any pre-existing _test.go.
//   - Each sub-case is independent — fresh ROKSBNKCTL_HOME + fresh
//     workspace seed; nothing leaks across cases.
//   - `flagOutput` is a persistent root flag and `flagTestHostsClearAuto`
//     is a package-scoped bool — both are reset by stageTestHostsFlags
//     so a stray value from a prior sub-case cannot bleed in.
//   - `promptYesNo` returns its default when stdin is not a TTY (see
//     `internal/cli/prompt.go`'s `isTTY()` guard); under `go test` the
//     stdin pipe is non-TTY, so the clear-prompt sub-case (h) needs no
//     mock — the default-No path runs naturally.

package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// runTestHostsCmd is the test-hosts-specific harness wrapper around
// runRootCmd. Staff's RunE family writes directly to os.Stdout /
// os.Stderr (not via cobra's cmd.OutOrStdout()), so the existing
// runRootCmd's cmd.SetOut/SetErr capture does NOT see those bytes.
// This wrapper pipes os.Stdout + os.Stderr through os.Pipe() for the
// duration of the call, drains them concurrently, and returns the
// captured strings alongside the cobra-execute error. Mirrors the
// captureStdout pattern in inspect_test.go but extended to both
// streams so the test-hosts assertions can grep stderr logs too.
func runTestHostsCmd(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	prevOut, prevErr := os.Stdout, os.Stderr
	rOut, wOut, perr := os.Pipe()
	if perr != nil {
		t.Fatalf("os.Pipe(stdout): %v", perr)
	}
	rErr, wErr, perr := os.Pipe()
	if perr != nil {
		t.Fatalf("os.Pipe(stderr): %v", perr)
	}
	os.Stdout = wOut
	os.Stderr = wErr

	outCh := make(chan string, 1)
	errCh := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, rOut)
		outCh <- buf.String()
	}()
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, rErr)
		errCh <- buf.String()
	}()

	// Drive cobra. runRootCmd ALSO captures via SetOut/SetErr — that
	// catches any cobra-internal error prints (the MinimumNArgs /
	// NoArgs rejection wording lands there, not on os.Stderr). The
	// returned cobraErrBuf is folded into our stderr capture so the
	// argv-strictness sub-cases (k, l) see the cobra wording in one
	// stream rather than two.
	_, cobraErrBuf, runErr := runRootCmd(t, args...)

	// Close the write halves so the io.Copy goroutines return.
	_ = wOut.Close()
	_ = wErr.Close()
	os.Stdout = prevOut
	os.Stderr = prevErr
	stdout = <-outCh
	stderr = <-errCh + cobraErrBuf
	_ = rOut.Close()
	_ = rErr.Close()
	return stdout, stderr, runErr
}

// stageTestHostsWorkspace seeds a fresh ROKSBNKCTL_HOME, writes a
// minimal workspace config to it, points `flagWorkspace` at that
// workspace, and resets the test-hosts-related flag globals (the
// persistent `-o/--output` flag on rootCmd and the local `--auto`
// flag on `test hosts clear`). Returns the workspace name + the
// home dir so the caller can stat config.yaml directly.
//
// The seed is non-nil and includes a region so config.LoadWorkspace
// round-trips cleanly. ExtraHosts left at its zero (nil) so the
// "empty config" sub-cases (a, e, f, j) start from a known baseline.
func stageTestHostsWorkspace(t *testing.T, seedHosts []string) (workspace, home string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv(config.ROKSBNKCTLHomeEnv, home)
	const ws = "test-hosts-ws"
	w := &config.Workspace{
		IBMCloud: config.IBMCloudCfg{Region: "us-south"},
	}
	if seedHosts != nil {
		w.Test.Connectivity.ExtraHosts = seedHosts
	}
	if err := config.SaveWorkspace(ws, w); err != nil {
		t.Fatalf("SaveWorkspace: %v", err)
	}
	resetTestHostsFlags(t)
	// Point flagWorkspace at the staged workspace so requireWorkspace()
	// inside each RunE resolves to it without needing to pass `-w` on
	// every invocation.
	prevWS := flagWorkspace
	flagWorkspace = ws
	t.Cleanup(func() { flagWorkspace = prevWS })
	return ws, home
}

// resetTestHostsFlags zeroes the package-global flag vars the test-
// hosts RunEs read, both before AND after the test, so a stray value
// from a previous sub-test cannot bleed into the run. Mirrors
// resetArgvFlags() / resetInitFlags() in the pre-existing _test.go
// files; kept under a distinct name so the three helpers don't
// collide on package-level imports.
func resetTestHostsFlags(t *testing.T) {
	t.Helper()
	prevOutput := flagOutput
	prevAuto := flagTestHostsClearAuto
	flagOutput = "text"
	flagTestHostsClearAuto = false
	t.Cleanup(func() {
		flagOutput = prevOutput
		flagTestHostsClearAuto = prevAuto
	})
}

// loadExtraHostsFromDisk re-reads the workspace's config.yaml and
// returns the ExtraHosts slice. Bypasses the in-process command
// machinery so the assertion is purely "what's on disk", proving the
// RunE persisted (or didn't).
func loadExtraHostsFromDisk(t *testing.T, workspace string) []string {
	t.Helper()
	ws, err := config.LoadWorkspace(workspace)
	if err != nil {
		t.Fatalf("loading workspace %q: %v", workspace, err)
	}
	return ws.Test.Connectivity.ExtraHosts
}

// ── (a) add persists ────────────────────────────────────────────────

// TestTestHostsAdd_Persists pins sub-case (a): `add <url>` against an
// empty workspace config persists the URL to
// `Workspace.Test.Connectivity.ExtraHosts`. Asserts via a fresh
// LoadWorkspace round-trip — the in-memory mutation alone is not
// enough; the test pins the on-disk persistence path.
func TestTestHostsAdd_Persists(t *testing.T) {
	ws, _ := stageTestHostsWorkspace(t, nil)

	_, errOut, runErr := runTestHostsCmd(t, "test", "hosts", "add", "https://docs.f5.com")
	if runErr != nil {
		t.Fatalf("(a) add failed: %v\nstderr:\n%s", runErr, errOut)
	}
	got := loadExtraHostsFromDisk(t, ws)
	want := []string{"https://docs.f5.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("(a) ExtraHosts after add = %v, want %v", got, want)
	}
}

// ── (b) add idempotent on already-present ───────────────────────────

// TestTestHostsAdd_Idempotent_AlreadyPresent pins sub-case (b): `add`
// against an already-present URL is a no-op (slice unchanged) and
// logs an "already present" line to stderr. The exit code stays 0 —
// idempotent re-adds are not errors.
func TestTestHostsAdd_Idempotent_AlreadyPresent(t *testing.T) {
	ws, _ := stageTestHostsWorkspace(t, []string{"https://docs.f5.com"})

	_, errOut, runErr := runTestHostsCmd(t, "test", "hosts", "add", "https://docs.f5.com")
	if runErr != nil {
		t.Fatalf("(b) add failed: %v\nstderr:\n%s", runErr, errOut)
	}
	got := loadExtraHostsFromDisk(t, ws)
	want := []string{"https://docs.f5.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("(b) ExtraHosts after idempotent add = %v, want %v", got, want)
	}
	// The "already present" log is operator-facing — pin the substring
	// so a future re-wording surfaces here.
	if !strings.Contains(errOut, "already present") {
		t.Errorf("(b) stderr must log 'already present' for idempotent re-add; got:\n%s", errOut)
	}
}

// ── (c) remove persists with order stability ────────────────────────

// TestTestHostsRemove_Persists pins sub-case (c): `remove <url>`
// against a present URL drops it from the slice AND preserves the
// remaining-order stability of the other entries (no re-sort).
func TestTestHostsRemove_Persists(t *testing.T) {
	ws, _ := stageTestHostsWorkspace(t, []string{
		"https://a.example.com",
		"https://b.example.com",
		"https://c.example.com",
	})

	_, errOut, runErr := runTestHostsCmd(t, "test", "hosts", "remove", "https://b.example.com")
	if runErr != nil {
		t.Fatalf("(c) remove failed: %v\nstderr:\n%s", runErr, errOut)
	}
	got := loadExtraHostsFromDisk(t, ws)
	want := []string{"https://a.example.com", "https://c.example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("(c) ExtraHosts after remove = %v, want %v (order must be preserved)", got, want)
	}
}

// ── (d) remove idempotent on absent ─────────────────────────────────

// TestTestHostsRemove_Idempotent_Absent pins sub-case (d): `remove`
// against an absent URL is a no-op (slice unchanged) and logs a "not
// present" line to stderr. Exit code stays 0 — absent removals are
// not errors.
func TestTestHostsRemove_Idempotent_Absent(t *testing.T) {
	ws, _ := stageTestHostsWorkspace(t, []string{"https://a.example.com"})

	_, errOut, runErr := runTestHostsCmd(t, "test", "hosts", "remove", "https://nope.example.com")
	if runErr != nil {
		t.Fatalf("(d) remove failed: %v\nstderr:\n%s", runErr, errOut)
	}
	got := loadExtraHostsFromDisk(t, ws)
	want := []string{"https://a.example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("(d) ExtraHosts after idempotent remove = %v, want %v", got, want)
	}
	if !strings.Contains(errOut, "not present") {
		t.Errorf("(d) stderr must log 'not present' for idempotent remove; got:\n%s", errOut)
	}
}

// ── (e) list empty → zero bytes + exit 0 ────────────────────────────

// TestTestHostsList_EmptyZeroBytes pins sub-case (e): `list` on an
// empty config emits zero bytes on stdout AND exit 0. The exit-0
// contract is the load-bearing half — distinguishing "nothing
// configured" from "command failed" by exit code (NOT by stderr
// scraping) is what makes the empty-list shape CI-safe.
func TestTestHostsList_EmptyZeroBytes(t *testing.T) {
	stageTestHostsWorkspace(t, nil)

	stdout, _, runErr := runTestHostsCmd(t, "test", "hosts", "list")
	if runErr != nil {
		t.Fatalf("(e) list on empty config must exit 0 (NOT an error); got: %v", runErr)
	}
	if stdout != "" {
		t.Errorf("(e) list on empty config must emit zero bytes; got %d bytes:\n%q", len(stdout), stdout)
	}
}

// ── (f) list --output json empty → `[]` ─────────────────────────────

// TestTestHostsList_EmptyJSONArray pins sub-case (f): `list -o json`
// on an empty config emits a valid JSON array (`[]`, not `null`). The
// shape is asserted by round-tripping through encoding/json — any
// well-formed JSON array of length 0 satisfies the contract.
func TestTestHostsList_EmptyJSONArray(t *testing.T) {
	stageTestHostsWorkspace(t, nil)

	stdout, _, runErr := runTestHostsCmd(t, "test", "hosts", "list", "-o", "json")
	if runErr != nil {
		t.Fatalf("(f) list -o json on empty config must exit 0; got: %v", runErr)
	}
	trimmed := strings.TrimSpace(stdout)
	var decoded []string
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		t.Fatalf("(f) stdout must decode as a JSON []string; got %q: %v", stdout, err)
	}
	if len(decoded) != 0 {
		t.Errorf("(f) JSON array on empty config must be length 0; got %v", decoded)
	}
	// Pin the literal `[]` shape too — a `null` would also decode to a
	// zero-length slice but the spec calls out `[]` explicitly (easier
	// for jq / CI parsers; staff's RunE coerces nil → []string{}).
	if trimmed != "[]" {
		t.Errorf("(f) empty JSON list must literally be `[]`, not %q (nil → []string{} coercion contract)", trimmed)
	}
}

// ── (g) list populated → newline URLs, insertion order ──────────────

// TestTestHostsList_PopulatedOrderStable pins sub-case (g): `list` on
// a populated config emits the URLs one per line on stdout in
// insertion order (no re-sort). Each line is bare (no prefix /
// numbering / table headers) — the raw shape is what makes piping
// into shell loops trivial.
func TestTestHostsList_PopulatedOrderStable(t *testing.T) {
	seed := []string{
		"https://docs.f5.com",
		"https://example.com",
		"https://api.openshift.com",
	}
	stageTestHostsWorkspace(t, seed)

	stdout, _, runErr := runTestHostsCmd(t, "test", "hosts", "list")
	if runErr != nil {
		t.Fatalf("(g) list on populated config failed: %v", runErr)
	}
	// Trim trailing newline (the final Fprintln adds one). The split
	// then gives the per-line slice.
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if !reflect.DeepEqual(lines, seed) {
		t.Errorf("(g) list output lines = %v, want %v (insertion-order, one per line)", lines, seed)
	}
}

// ── (h) clear prompt defaults to No (no --auto, non-TTY) ────────────

// TestTestHostsClear_PromptDefaultsNo pins sub-case (h): without
// `--auto`, `clear` calls promptYesNo() with default=false. Under
// `go test`, stdin is not a TTY, so promptYesNo returns its default
// (false) without reading any input — the clear should be declined
// and the slice unchanged. This is the load-bearing safety property:
// an operator who accidentally types `test hosts clear` instead of
// `test hosts list` does NOT lose their config.
func TestTestHostsClear_PromptDefaultsNo(t *testing.T) {
	seed := []string{"https://a.example.com", "https://b.example.com"}
	ws, _ := stageTestHostsWorkspace(t, seed)

	_, errOut, runErr := runTestHostsCmd(t, "test", "hosts", "clear")
	if runErr != nil {
		t.Fatalf("(h) clear without --auto failed: %v\nstderr:\n%s", runErr, errOut)
	}
	got := loadExtraHostsFromDisk(t, ws)
	if !reflect.DeepEqual(got, seed) {
		t.Errorf("(h) ExtraHosts after declined clear = %v, want %v (slice MUST be unchanged when prompt defaults to No)", got, seed)
	}
}

// ── (i) clear --auto skips prompt + nils the slice ──────────────────

// TestTestHostsClear_AutoSkipsPrompt pins sub-case (i): `clear --auto`
// bypasses the confirmation prompt and clears the slice. Staff's
// implementation sets ExtraHosts to nil (the yaml:omitempty tag means
// the YAML key is omitted entirely; loading back yields nil). Either
// nil or an empty (non-nil) slice would satisfy the operator-visible
// contract — both render as zero bytes via `list`.
func TestTestHostsClear_AutoSkipsPrompt(t *testing.T) {
	seed := []string{"https://a.example.com", "https://b.example.com"}
	ws, home := stageTestHostsWorkspace(t, seed)

	_, errOut, runErr := runTestHostsCmd(t, "test", "hosts", "clear", "--auto")
	if runErr != nil {
		t.Fatalf("(i) clear --auto failed: %v\nstderr:\n%s", runErr, errOut)
	}
	got := loadExtraHostsFromDisk(t, ws)
	if len(got) != 0 {
		t.Errorf("(i) ExtraHosts after `clear --auto` must be empty/nil; got %v", got)
	}
	// Belt-and-suspenders: the YAML on disk must not still carry the
	// extra_hosts key (omitempty on a nil slice). Read the raw bytes —
	// any sub-string mention of `extra_hosts` is a regression.
	cfgPath := filepath.Join(home, ws, "config.yaml")
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("(i) reading %s: %v", cfgPath, err)
	}
	if strings.Contains(string(raw), "extra_hosts") {
		t.Errorf("(i) config.yaml must not carry `extra_hosts` after `clear --auto` (omitempty on nil slice); got:\n%s", string(raw))
	}
}

// ── (j) add non-URL errors with actionable message ──────────────────

// TestTestHostsAdd_NonURL_Errors pins sub-case (j): `add` with a
// non-URL arg (e.g. bare text, no scheme) returns a non-nil error
// AND the slice is unchanged on disk. Staff's `validateHostURL`
// rejects empty + scheme-less + host-less inputs; the test pins the
// scheme-less shape ("not a url with spaces") which is the operator's
// most common typo (forgetting `https://`). The error message must
// name the offending arg so the operator can find their typo without
// scrolling.
func TestTestHostsAdd_NonURL_Errors(t *testing.T) {
	ws, _ := stageTestHostsWorkspace(t, nil)
	const bogus = "not a url with spaces"

	_, errOut, runErr := runTestHostsCmd(t, "test", "hosts", "add", bogus)
	if runErr == nil {
		t.Fatalf("(j) add %q must error; got nil\nstderr:\n%s", bogus, errOut)
	}
	combined := runErr.Error() + "\n" + errOut
	if !strings.Contains(combined, "invalid host URL") {
		t.Errorf("(j) error must surface `invalid host URL` for non-URL arg; got:\n%s", combined)
	}
	// Slice must be unchanged — fail-before-write discipline.
	got := loadExtraHostsFromDisk(t, ws)
	if len(got) != 0 {
		t.Errorf("(j) ExtraHosts must be unchanged after rejected add; got %v", got)
	}
}

// ── (k) cobra.NoArgs pin on list + clear ────────────────────────────

// TestTestHostsArgs_NoArgs_ListAndClear pins sub-case (k): both
// `list` and `clear` declare `Args: cobra.NoArgs`; stray positionals
// trip cobra's ValidateArgs BEFORE any RunE runs. Mirrors the Sprint
// 21 `argv_strictness_test.go` pattern (cobra phrases the rejection
// one of two equivalent ways — either the offending positional is
// named, or the canonical "accepts 0 arg(s)" wording surfaces).
func TestTestHostsArgs_NoArgs_ListAndClear(t *testing.T) {
	cases := []struct {
		name string
		argv []string
	}{
		// (k1) `test hosts list foo` — stray positional `foo`
		{"list_stray", []string{"test", "hosts", "list", "foo"}},
		// (k2) `test hosts clear bar` — stray positional `bar`
		{"clear_stray", []string{"test", "hosts", "clear", "bar"}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			stageTestHostsWorkspace(t, nil)

			_, errOut, runErr := runTestHostsCmd(t, c.argv...)
			if runErr == nil {
				t.Fatalf("(k)/%s: expected non-zero exit on `%s`; got nil\nstderr:\n%s",
					c.name, strings.Join(c.argv, " "), errOut)
			}
			combined := runErr.Error() + "\n" + errOut
			if !strings.Contains(combined, "unknown command") &&
				!strings.Contains(combined, "accepts 0 arg") {
				t.Errorf("(k)/%s: error must surface a cobra NoArgs / unknown-command message; got:\n%s",
					c.name, combined)
			}
		})
	}
}

// ── (l) cobra.MinimumNArgs(1) pin on add + remove ───────────────────

// TestTestHostsArgs_MinimumNArgs_AddAndRemove pins sub-case (l): both
// `add` and `remove` declare `Args: cobra.MinimumNArgs(1)`; invoking
// either with zero positionals trips cobra's ValidateArgs BEFORE any
// RunE runs. Cobra's standard wording for this constraint is
// "requires at least 1 arg(s), only received 0" — the test accepts
// any phrasing that mentions "at least" or the literal arg count.
func TestTestHostsArgs_MinimumNArgs_AddAndRemove(t *testing.T) {
	cases := []struct {
		name string
		argv []string
	}{
		// (l1) `test hosts add` — zero positionals
		{"add_zero", []string{"test", "hosts", "add"}},
		// (l2) `test hosts remove` — zero positionals
		{"remove_zero", []string{"test", "hosts", "remove"}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			stageTestHostsWorkspace(t, nil)

			_, errOut, runErr := runTestHostsCmd(t, c.argv...)
			if runErr == nil {
				t.Fatalf("(l)/%s: expected non-zero exit on `%s`; got nil\nstderr:\n%s",
					c.name, strings.Join(c.argv, " "), errOut)
			}
			combined := runErr.Error() + "\n" + errOut
			if !strings.Contains(combined, "requires at least") &&
				!strings.Contains(combined, "1 arg") {
				t.Errorf("(l)/%s: error must surface a cobra MinimumNArgs rejection (`requires at least` / `1 arg`); got:\n%s",
					c.name, combined)
			}
		})
	}
}
