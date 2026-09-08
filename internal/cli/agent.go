package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"golang.org/x/term"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/embedded"
)

// roksbnkctl's "agentic mode" (mirrors the dpubnkctl pattern): the binary
// embeds no LLM. It scaffolds a workspace with AGENTS.md + persona role
// contracts (`agent init`), and prints the invocation to launch the operator's
// preferred coding-agent CLI pointed at them (`agent <cli>`). The CLI itself
// stays a deterministic tool; the agentic layer is markdown + a launcher.

// agentRecipes maps an agentic CLI name to the invocation to print. Each cd's
// into the workspace dir (which holds AGENTS.md + personas/) and starts the
// agent under the solution-architect persona.
var agentRecipes = map[string]func(dir, endpoint string) string{
	"claude": func(dir, endpoint string) string {
		extra := ""
		if endpoint != "" {
			extra = "\n  ANTHROPIC_BASE_URL=" + endpoint + " \\"
		}
		return fmt.Sprintf(`# Claude Code (https://docs.claude.com/en/docs/claude-code)
cd %s && \%s
  claude
# Then say:
#   "Read AGENTS.md, then act as the solution-architect persona
#    (personas/solution-architect.md). Confirm scope with me."
`, dir, extra)
	},
	"gemini": func(dir, endpoint string) string {
		return fmt.Sprintf(`# Gemini CLI
cd %s && \
  gemini chat --system-instruction "$(cat AGENTS.md)"
# Then point Gemini at personas/solution-architect.md to start.
`, dir)
	},
	"aider": func(dir, endpoint string) string {
		base := ""
		if endpoint != "" {
			base = " --openai-api-base " + endpoint
		}
		return fmt.Sprintf(`# Aider
cd %s && \
  aider --read AGENTS.md --read personas/solution-architect.md%s \
        config.yaml decisions.md
`, dir, base)
	},
	"openai": func(dir, endpoint string) string {
		base := endpoint
		if base == "" {
			base = "https://api.openai.com/v1  # or your local vLLM endpoint"
		}
		return fmt.Sprintf(`# Generic OpenAI-compatible REPL (e.g., simonw/llm, chatgpt-cli)
cd %s
export OPENAI_API_BASE=%s
# Load AGENTS.md as the system prompt and start with personas/solution-architect.md.
# Example with simonw/llm:
#   llm --system "$(cat AGENTS.md)" "Act as solution-architect; read config.yaml; confirm scope."
`, dir, base)
	},
	"pi": func(dir, endpoint string) string {
		return fmt.Sprintf(`# pi coding agent (https://pi.dev/)
cd %s && \
  pi
# pi auto-loads AGENTS.md from this directory (and parent dirs).
# To add the persona inline:
#   pi --append-system-prompt "$(cat personas/solution-architect.md)"
`, dir)
	},
	"agy": func(dir, endpoint string) string {
		return fmt.Sprintf(`# agy (Google)
cd %s && \
  agy -i "Read AGENTS.md, then act as the solution-architect persona \
(personas/solution-architect.md). Confirm scope with me."
# -i runs that prompt and CONTINUES interactively, so the persona is loaded
# without relying on agy picking AGENTS.md up by itself.
# Useful flags: --model, --effort low|medium|high, --add-dir <path>,
# --mode plan to plan before editing.
`, dir)
	},
	"opencode": func(dir, endpoint string) string {
		return fmt.Sprintf(`# OpenCode (https://opencode.ai/)
cd %s && \
  opencode
# AGENTS.md in this dir is opencode's project-config convention — auto-loaded.
# Pick a non-Anthropic model with --model (Claude models are blocked for
# opencode since 2026-01), e.g. --model openrouter/google/gemini-2.5-pro.
`, dir)
	},
}

var agentCmd = &cobra.Command{
	Use:   "agent [claude|gemini|aider|openai|pi|opencode|agy]",
	Short: "Drive this workspace with an agentic CLI (personas + AGENTS.md)",
	Long: `Agentic mode. roksbnkctl embeds no LLM — bring your own coding-agent CLI.

  roksbnkctl agent init        Scaffold AGENTS.md + personas/ + journal/ into the workspace
  roksbnkctl agent             List supported CLIs + this workspace's default
  roksbnkctl agent <cli>       Launch <cli> against the workspace\n  roksbnkctl agent <cli> --show  Print the invocation instead of running it

Personas (act as exactly one at a time): solution-architect (customer
interface, owns scope), cloud-operator (runs the lifecycle), test-engineer
(validation probes), doc-specialist (the report). See personas/ after init.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runAgent,
}

var agentInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Scaffold the agentic-mode files into the workspace",
	Long: `Copies AGENTS.md, CLAUDE.md, the persona role contracts, and a
decisions.md seed into the workspace dir, and creates an empty journal/. Safe to
re-run: existing files are left untouched (your edits survive).`,
	Args: cobra.NoArgs,
	RunE: runAgentInit,
}

var flagAgentShow bool

func init() {
	agentCmd.Flags().BoolVar(&flagAgentShow, "show", false,
		"print the invocation instead of running the agent")
	agentCmd.AddCommand(agentInitCmd)
	rootCmd.AddCommand(agentCmd)
}

// agentDefault resolves the workspace's configured default agentic CLI, or
// "claude" when unset.
func agentDefault(ws *config.Workspace) string {
	if ws != nil && ws.Agent != nil && ws.Agent.Default != "" {
		return ws.Agent.Default
	}
	return "claude"
}

func runAgent(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()

	// Load the workspace best-effort — the bare listing form is useful even
	// without a fully-initialised workspace.
	dir, derr := config.WorkspaceDir(resolvedWorkspaceName())
	var ws *config.Workspace
	if cctx, err := config.New(flagWorkspace); err == nil {
		ws = cctx.Workspace
	}
	endpoint := ""
	if ws != nil && ws.Agent != nil {
		endpoint = ws.Agent.LLMEndpoint
	}

	if len(args) == 0 {
		fmt.Fprintln(out, "Supported agentic CLIs:")
		for _, name := range agentRecipeNames() {
			fmt.Fprintf(out, "  - %s\n", name)
		}
		fmt.Fprintf(out, "\nDefault for this workspace: %s\n", agentDefault(ws))
		fmt.Fprintln(out, "\nRun:  roksbnkctl agent <name>          to launch it,")
		fmt.Fprintln(out, "      roksbnkctl agent <name> --show   to print the invocation instead,")
		fmt.Fprintln(out, "      roksbnkctl agent init            to scaffold the persona files first.")
		return nil
	}

	if derr != nil {
		return derr
	}
	recipe, ok := agentRecipes[args[0]]
	if !ok {
		return fmt.Errorf("unknown agent %q (try: %v)", args[0], agentRecipeNames())
	}
	if flagAgentShow {
		fmt.Fprint(out, recipe(dir, endpoint))
		if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err != nil {
			fmt.Fprintf(out, "\n# NOTE: %s has no AGENTS.md yet — run `roksbnkctl agent init` first.\n", dir)
		}
		return nil
	}
	return launchAgent(cmd, args[0], dir, endpoint)
}

func runAgentInit(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	dir, err := config.WorkspaceDir(resolvedWorkspaceName())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating workspace dir %s: %w", dir, err)
	}

	written, skipped, err := copyEmbeddedFiles(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "journal"), 0o755); err != nil {
		return fmt.Errorf("creating journal dir: %w", err)
	}

	// Seed an initial journal entry (only if today's init entry isn't there).
	jpath := filepath.Join(dir, "journal", time.Now().UTC().Format("2006-01-02")+"-init.md")
	if _, statErr := os.Stat(jpath); os.IsNotExist(statErr) {
		entry := fmt.Sprintf("## %s — agentic mode initialised\n\nWorkspace scaffolded with AGENTS.md + personas/. Start with the solution-architect persona and confirm scope against config.yaml.\n",
			time.Now().UTC().Format("2006-01-02 15:04 UTC"))
		if werr := os.WriteFile(jpath, []byte(entry), 0o644); werr == nil {
			written = append(written, "journal/"+filepath.Base(jpath))
		}
	}

	for _, f := range written {
		fmt.Fprintf(out, "  + %s\n", f)
	}
	for _, f := range skipped {
		fmt.Fprintf(out, "  = %s (exists, left as-is)\n", f)
	}
	fmt.Fprintf(out, "\n✓ Agentic mode ready in %s\n", dir)
	fmt.Fprintf(out, "  Next: roksbnkctl -w %s agent %s\n", resolvedWorkspaceName(), agentDefault(nil))
	return nil
}

// copyEmbeddedFiles walks the embedded files/ tree and writes each file under
// dir, preserving the relative path. Existing files are skipped (so re-running
// `agent init` never clobbers operator edits). Returns the written + skipped
// relative paths.
func copyEmbeddedFiles(dir string) (written, skipped []string, err error) {
	const root = "files"
	walkErr := fs.WalkDir(embedded.FS, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		dst := filepath.Join(dir, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		if _, statErr := os.Stat(dst); statErr == nil {
			skipped = append(skipped, rel)
			return nil
		}
		data, readErr := embedded.FS.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if mkErr := os.MkdirAll(filepath.Dir(dst), 0o755); mkErr != nil {
			return mkErr
		}
		if wErr := os.WriteFile(dst, data, 0o644); wErr != nil {
			return wErr
		}
		written = append(written, rel)
		return nil
	})
	return written, skipped, walkErr
}

// agentRecipeNames returns the supported CLI names in a stable order.
func agentRecipeNames() []string {
	return []string{"claude", "gemini", "aider", "openai", "pi", "opencode", "agy"}
}

// ── launching ───────────────────────────────────────────────────────────────
//
// `roksbnkctl agent <cli>` RUNS the agent; `--show` prints the invocation
// instead. It used to only ever print, which left every user doing the same
// dance -- read the recipe, paste it -- and the obvious shortcut for that,
// `roksbnkctl agent <cli> | bash`, is silently WRONG: bash's stdin is the pipe
// carrying the recipe, so the agent's first "user turn" is the remaining comment
// lines of its own recipe. It does not error. It just starts a session having
// apparently been told "# -i runs that prompt and CONTINUES interactively."
//
// Running it here also brings `agent` in line with the kubectl/oc/ibmcloud
// passthroughs, which have always exec'd a real tool. The "roksbnkctl embeds no
// LLM" property is unchanged: exec'ing someone else's CLI is not embedding one,
// any more than `roksbnkctl kubectl` embeds Kubernetes.

// agentArgv builds the command to exec for a recipe, or returns errNotRunnable
// when the recipe is guidance rather than a single command.
var errNotRunnable = errors.New("this recipe is guidance, not a single command")

// agentBinary is the executable each recipe launches, used for the
// "is it installed" check before anything else happens.
var agentBinary = map[string]string{
	"claude": "claude", "gemini": "gemini", "aider": "aider",
	"pi": "pi", "opencode": "opencode", "agy": "agy",
	// "openai" is deliberately absent: its recipe is an export plus commented
	// examples for whichever OpenAI-compatible REPL you use, not a command.
}

func agentArgv(name, dir, endpoint string) ([]string, []string, error) {
	env := os.Environ()
	switch name {
	case "claude":
		if endpoint != "" {
			env = append(env, "ANTHROPIC_BASE_URL="+endpoint)
		}
		return []string{"claude"}, env, nil
	case "gemini":
		sys, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
		if err != nil {
			return nil, nil, fmt.Errorf("gemini needs AGENTS.md as its system instruction: %w", err)
		}
		return []string{"gemini", "chat", "--system-instruction", string(sys)}, env, nil
	case "aider":
		argv := []string{"aider", "--read", "AGENTS.md", "--read", "personas/solution-architect.md"}
		if endpoint != "" {
			argv = append(argv, "--openai-api-base", endpoint)
		}
		return append(argv, "config.yaml", "decisions.md"), env, nil
	case "pi":
		return []string{"pi"}, env, nil
	case "opencode":
		return []string{"opencode"}, env, nil
	case "agy":
		return []string{"agy", "-i", agentPersonaPrompt}, env, nil
	}
	return nil, nil, errNotRunnable
}

// agentPersonaPrompt is the seed turn for agents that do not auto-load
// AGENTS.md. Kept identical to the text the --show recipe prints, so what runs
// and what is documented cannot drift.
const agentPersonaPrompt = "Read AGENTS.md, then act as the solution-architect persona " +
	"(personas/solution-architect.md). Confirm scope with me."

// stdoutIsTerminal is a variable so the refusal below can be tested in both
// directions. Under `go test` stdout is never a terminal, so without this seam
// the only reachable branch is the refusal, and the launch path would be
// asserted by nothing.
var stdoutIsTerminal = func() bool { return term.IsTerminal(int(os.Stdout.Fd())) }

// launchAgent execs the agent in dir with stdio inherited.
//
// It REFUSES when stdout is not a terminal. That guard exists for one specific
// reason: before this change the command only printed, so `eval "$(roksbnkctl
// agent claude)"` was the documented way to run it. If that line survives in
// someone's notes and the default flips under it, the agent runs with its stdout
// captured by $(), and whatever an LLM emits gets evaluated as shell. Failing
// loudly is the only acceptable behaviour there.
func launchAgent(cmd *cobra.Command, name, dir, endpoint string) error {
	argv, env, err := agentArgv(name, dir, endpoint)
	if errors.Is(err, errNotRunnable) {
		return fmt.Errorf("`%s` prints guidance rather than a single command, so there is "+
			"nothing to run.\nUse `roksbnkctl agent %s --show` and follow it.", name, name)
	}
	if err != nil {
		return err
	}

	if !stdoutIsTerminal() {
		return fmt.Errorf("refusing to launch %s: stdout is not a terminal.\n"+
			"An agent session needs one, and capturing it — `$(roksbnkctl agent %s)` or a "+
			"pipe — would feed the agent's own output somewhere it does not belong.\n"+
			"To see the invocation instead: roksbnkctl agent %s --show", name, name, name)
	}

	bin := agentBinary[name]
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("%s is not on PATH. Install it, or print the invocation with "+
			"`roksbnkctl agent %s --show`", bin, name)
	}

	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err != nil {
		return fmt.Errorf("%s has no AGENTS.md — run `roksbnkctl agent init` first "+
			"(the agent would start with no persona and no working agreements)", dir)
	}

	fmt.Fprintf(cmd.ErrOrStderr(), "→ %s in %s\n", bin, dir)
	c := exec.CommandContext(cmdContext(cmd), argv[0], argv[1:]...)
	c.Dir = dir
	c.Env = env
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}
