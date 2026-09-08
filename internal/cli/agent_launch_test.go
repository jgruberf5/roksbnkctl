package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// newAgentTestCmd is a cobra command with its output captured, so launchAgent
// can be called without writing to the test runner's streams.
func newAgentTestCmd() *cobra.Command {
	c := &cobra.Command{}
	c.SetOut(&bytes.Buffer{})
	c.SetErr(&bytes.Buffer{})
	return c
}

// `roksbnkctl agent <cli>` now RUNS the agent; --show prints the invocation.
// These cover the launch layer. The recipe text itself is covered by
// TestAgentRecipesAllRender and the agy tests.

// TestEveryRunnableRecipeHasArgv pins the two maps together. agentBinary says
// which executable to look for and agentArgv builds the command; they are
// separate maps, so one can gain an entry the other lacks. The failure is not a
// crash -- a recipe with argv but no binary entry looks up "" on PATH, and one
// with a binary but no argv reports "guidance, not a command" about a recipe that
// is a perfectly good command.
func TestEveryRunnableRecipeHasArgv(t *testing.T) {
	for _, name := range agentRecipeNames() {
		_, hasBin := agentBinary[name]
		_, _, err := agentArgv(name, t.TempDir(), "")
		runnable := !errors.Is(err, errNotRunnable)

		// gemini's argv reads AGENTS.md and fails in a TempDir; that is a real
		// error, not "not runnable", so it still counts as runnable.
		if name == "gemini" {
			runnable = true
		}
		if hasBin != runnable {
			t.Errorf("%s: agentBinary=%v but argv-runnable=%v — the two maps disagree",
				name, hasBin, runnable)
		}
	}
}

// TestOpenAIIsNotRunnable pins the one deliberate exception. Its recipe is an
// export plus commented examples for whichever OpenAI-compatible REPL you use.
// If it ever gains an argv, it must gain a real command too, not a guess.
func TestOpenAIIsNotRunnable(t *testing.T) {
	_, _, err := agentArgv("openai", t.TempDir(), "")
	if !errors.Is(err, errNotRunnable) {
		t.Errorf("openai argv error = %v, want errNotRunnable", err)
	}
}

// TestAgyArgvMatchesItsShownRecipe is the drift guard that matters. The persona
// prompt now exists twice: in the recipe --show prints, and in the argv the
// launch path execs. If they diverge, --show documents one thing and the tool
// does another -- and nothing fails, because both are individually valid.
func TestAgyArgvMatchesItsShownRecipe(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	argv, _, err := agentArgv("agy", dir, "")
	if err != nil {
		t.Fatalf("agy argv: %v", err)
	}
	if len(argv) != 3 || argv[0] != "agy" || argv[1] != "-i" {
		t.Fatalf("agy argv = %v, want [agy -i <prompt>]", argv)
	}
	shown := agentRecipes["agy"](dir, "")
	// The recipe wraps the prompt across two lines with a backslash continuation;
	// undo that the way the shell does before comparing.
	unwrapped := strings.ReplaceAll(shown, "\\\n", "")
	if !strings.Contains(unwrapped, argv[2]) {
		t.Errorf("the prompt agy is EXECUTED with is not the one --show prints.\n"+
			"exec: %q\nshown:\n%s", argv[2], shown)
	}
}

// TestClaudeEndpointReachesTheEnvironment: --show weaves ANTHROPIC_BASE_URL into
// the printed recipe, so the launch path has to actually set it. A configured
// llm_endpoint that only appeared in the printout would send the session to the
// default vendor while the recipe claimed otherwise.
func TestClaudeEndpointReachesTheEnvironment(t *testing.T) {
	_, env, err := agentArgv("claude", t.TempDir(), "https://llm.local/v1")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, kv := range env {
		if kv == "ANTHROPIC_BASE_URL=https://llm.local/v1" {
			found = true
		}
	}
	if !found {
		t.Error("claude launch env lacks ANTHROPIC_BASE_URL; --show advertises it")
	}

	_, env2, _ := agentArgv("claude", t.TempDir(), "")
	for _, kv := range env2 {
		if strings.HasPrefix(kv, "ANTHROPIC_BASE_URL=") && kv != "ANTHROPIC_BASE_URL="+os.Getenv("ANTHROPIC_BASE_URL") {
			t.Errorf("no endpoint configured but launch env sets %q", kv)
		}
	}
}

// TestAiderEndpointReachesArgv: aider takes the endpoint as a flag rather than an
// env var, so the same question has a different answer and needs its own check.
func TestAiderEndpointReachesArgv(t *testing.T) {
	argv, _, err := agentArgv("aider", t.TempDir(), "https://llm.local/v1")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "--openai-api-base https://llm.local/v1") {
		t.Errorf("aider argv lacks the endpoint flag: %v", argv)
	}
	plain, _, _ := agentArgv("aider", t.TempDir(), "")
	if strings.Contains(strings.Join(plain, " "), "--openai-api-base") {
		t.Errorf("no endpoint configured but aider argv passes one: %v", plain)
	}
}

// TestAgentRefusesWhenStdoutIsNotATerminal is the safety property of this change,
// and the reason flipping the default is not reckless.
//
// Before this, `agent <cli>` only printed, so `eval "$(roksbnkctl agent claude)"`
// was the documented way to run one. If that line survives in someone's notes,
// the flipped default would run the agent with stdout captured by $() -- and
// whatever the model emitted would be evaluated as shell. Refusing is the only
// acceptable behaviour, and it has to be checked, not assumed.
func TestAgentRefusesWhenStdoutIsNotATerminal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	prev := stdoutIsTerminal
	stdoutIsTerminal = func() bool { return false }
	t.Cleanup(func() { stdoutIsTerminal = prev })

	err := launchAgent(newAgentTestCmd(), "agy", dir, "")
	if err == nil {
		t.Fatal("launched with stdout captured — an eval'd session would evaluate model output as shell")
	}
	for _, want := range []string{"not a terminal", "--show"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q: %v", want, err)
		}
	}
}

// The refusal must not be the ONLY reachable branch: with a terminal, launching
// proceeds far enough to look the binary up. Without this, deleting the guard
// entirely would still leave the test above passing for the wrong reason.
func TestAgentProceedsPastTheTerminalCheckWithATerminal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	prev := stdoutIsTerminal
	stdoutIsTerminal = func() bool { return true }
	t.Cleanup(func() { stdoutIsTerminal = prev })

	// A binary that certainly is not installed: the run must fail on LOOKUP,
	// which proves the terminal check was passed rather than short-circuited.
	err := launchAgent(newAgentTestCmd(), "opencode", dir, "")
	if err == nil {
		t.Skip("opencode is installed on this host; the lookup path cannot be asserted")
	}
	if strings.Contains(err.Error(), "not a terminal") {
		t.Errorf("terminal check rejected a terminal: %v", err)
	}
	if !strings.Contains(err.Error(), "not on PATH") {
		t.Errorf("expected the PATH lookup to be reached, got: %v", err)
	}
}
