package cli

// benchmark_scenarios.go — faithful Go port of forge's benchmark_scenarios.py
// expand_scenario engine (WS-C1 of ADR-0001).
//
// The public API mirrors the Python module:
//   - forgeScenarioRegistry — keyed map of all 8 synthetic presets
//   - expandForgeScenario(key, base)  — expands a key into ordered child configs
//
// Values (concurrency sweeps, request-count formulas, seq-dist strings, prefix
// lengths, extra-inputs) are kept byte-identical to the Python source so that
// a future migration to forge-agent orchestration (architecture I) produces
// identical workloads.
//
// mooncake (WS-C2: trace download + 0.80 time-dilation + tokenizer substitution)
// is OUT OF SCOPE for this task. Its registry shape is stubbed so WS-C2 can slot
// in later; it is rejected by expandForgeScenario with a clear error.
//
// Reference source:
//   bnk-forge-v2/backend/services/benchmark_scenarios.py

import (
	"fmt"

	"github.com/JLCode-tech/awsbnkctl/internal/jumphost"
)

// syntheticModel is the default LLM targeted by all synthetic scenarios.
// Mirror of benchmark_scenarios.py SYNTHETIC_MODEL.
const syntheticModel = "meta-llama/Llama-3.1-70B-Instruct"

// defaultSweep is the standard concurrency sweep used by most scenarios.
// Mirror of benchmark_scenarios.py DEFAULT_SWEEP = (50, 100, 150, 200).
var defaultSweep = []int{50, 100, 150, 200}

// heavyConcISL is the high (concurrency, ISL) pair list used by high-concurrency
// and prefix-cache scenarios. Mirror of benchmark_scenarios.py HEAVY_CONC_ISL.
var heavyConcISL = [][2]int{{150, 5000}, {200, 7000}, {250, 9000}, {300, 10000}}

// rcMin computes request count: max(c*5, 20).
// Mirror of benchmark_scenarios.py _rc_min(c, mult=5, floor=20).
func rcMin(c int) int {
	v := c * 5
	if v < 20 {
		return 20
	}
	return v
}

// syntheticBase returns the base AiperfConfig fields shared by all synthetic
// scenarios. Mirror of benchmark_scenarios.py _synthetic_base().
//
// streaming=true and extra_inputs=["ignore_eos:true"] are part of the base;
// model and endpoint_type may be overridden by the caller.
func syntheticBase(endpointType string) jumphost.AiperfConfig {
	if endpointType == "" {
		endpointType = "chat"
	}
	return jumphost.AiperfConfig{
		Model:        syntheticModel,
		EndpointType: endpointType,
		Streaming:    true,
		ExtraInputs:  []string{"ignore_eos:true"},
	}
}

// forgeScenarioChild is one child variant produced by expandForgeScenario.
// Each child maps to a single aiperf invocation.
type forgeScenarioChild struct {
	// VariantLabel is the per-child identifier (e.g. "c150-isl5000").
	// Used to compose the run label: <base>-<scenario>-<variantLabel>.
	VariantLabel string
	// Phase is an optional phase name (e.g. "warmup", "short", "long",
	// "burst", "probe"). Informational only; not an aiperf flag.
	Phase string
	// Turn is the multi-turn conversation turn number. 0 means unset.
	Turn int
	// Round is the burst-recovery round number. 0 means unset.
	Round int
	// Config is the merged aiperf configuration for this child.
	Config jumphost.AiperfConfig
}

// forgeScenario is the Go equivalent of benchmark_scenarios.py ScenarioPreset.
type forgeScenario struct {
	Key         string
	Name        string
	Description string
	Tags        []string
	TraceDriven bool // true for mooncake — cannot be executed in WS-C1
	// buildVariants returns the ordered child list (pure function, no I/O).
	buildVariants func() []forgeScenarioChild
}

// ---------------------------------------------------------------------------
// Variant builders — one per scenario (mirrors the _*_variants() functions)
// ---------------------------------------------------------------------------

func baselineVariants() []forgeScenarioChild {
	out := make([]forgeScenarioChild, len(defaultSweep))
	for i, c := range defaultSweep {
		cfg := syntheticBase("")
		cfg.Concurrency = c
		cfg.NumRequests = rcMin(c)
		cfg.ISL = 500
		cfg.OSL = 128
		out[i] = forgeScenarioChild{
			VariantLabel: fmt.Sprintf("c%d", c),
			Config:       cfg,
		}
	}
	return out
}

func highConcurrencyVariants() []forgeScenarioChild {
	out := make([]forgeScenarioChild, len(heavyConcISL))
	for i, pair := range heavyConcISL {
		c, isl := pair[0], pair[1]
		cfg := syntheticBase("")
		cfg.Concurrency = c
		cfg.NumRequests = c * 5
		cfg.ISL = isl
		cfg.OSL = 128
		out[i] = forgeScenarioChild{
			VariantLabel: fmt.Sprintf("c%d-isl%d", c, isl),
			Config:       cfg,
		}
	}
	return out
}

func mixedWorkloadVariants() []forgeScenarioChild {
	var out []forgeScenarioChild

	// Warmup phase: fixed c=50
	warmupCfg := syntheticBase("")
	warmupCfg.Concurrency = 50
	warmupCfg.NumRequests = 150
	warmupCfg.ISL = 500
	warmupCfg.OSL = 128
	out = append(out, forgeScenarioChild{
		VariantLabel: "warmup",
		Phase:        "warmup",
		Config:       warmupCfg,
	})

	// Short phase: sweep, ISL 500, OSL 64
	for _, c := range defaultSweep {
		cfg := syntheticBase("")
		cfg.Concurrency = c
		cfg.NumRequests = c * 5
		cfg.ISL = 500
		cfg.OSL = 64
		out = append(out, forgeScenarioChild{
			VariantLabel: fmt.Sprintf("short-c%d", c),
			Phase:        "short",
			Config:       cfg,
		})
	}

	// Long phase: sweep, ISL 600, OSL 128, prefix
	for _, c := range defaultSweep {
		cfg := syntheticBase("")
		cfg.Concurrency = c
		cfg.NumRequests = c * 5
		cfg.ISL = 600
		cfg.OSL = 128
		cfg.PrefixPromptLength = 1400
		cfg.NumPrefixPrompts = 10
		out = append(out, forgeScenarioChild{
			VariantLabel: fmt.Sprintf("long-c%d", c),
			Phase:        "long",
			Config:       cfg,
		})
	}

	return out
}

func multiTurnVariants() []forgeScenarioChild {
	var out []forgeScenarioChild

	// Turn 1: no prefix
	for _, c := range defaultSweep {
		cfg := syntheticBase("")
		cfg.Concurrency = c
		cfg.NumRequests = c * 5
		cfg.ISL = 500
		cfg.OSL = 128
		out = append(out, forgeScenarioChild{
			VariantLabel: fmt.Sprintf("turn1-c%d", c),
			Turn:         1,
			Config:       cfg,
		})
	}

	// Turns 2-4: growing prefix lengths
	for _, tp := range [][2]int{{2, 500}, {3, 1000}, {4, 1500}} {
		turn, prefixLen := tp[0], tp[1]
		for _, c := range defaultSweep {
			cfg := syntheticBase("")
			cfg.Concurrency = c
			cfg.NumRequests = c * 5
			cfg.ISL = 500
			cfg.OSL = 128
			cfg.PrefixPromptLength = prefixLen
			cfg.NumPrefixPrompts = 10
			out = append(out, forgeScenarioChild{
				VariantLabel: fmt.Sprintf("turn%d-c%d", turn, c),
				Turn:         turn,
				Config:       cfg,
			})
		}
	}

	return out
}

func prefixCacheVariants() []forgeScenarioChild {
	// 80% shared prefix: prefix=ISL*8//10, unique=ISL-prefix, stddev=unique//10, 20 groups.
	// Mirror of benchmark_scenarios.py _prefix_cache_variants().
	out := make([]forgeScenarioChild, len(heavyConcISL))
	for i, pair := range heavyConcISL {
		c, isl := pair[0], pair[1]
		prefixLen := isl * 8 / 10
		uniqueLen := isl - prefixLen
		cfg := syntheticBase("chat")
		cfg.Concurrency = c
		cfg.NumRequests = c * 5
		cfg.ISL = uniqueLen
		cfg.ISLStddev = uniqueLen / 10
		cfg.OSL = 128
		cfg.NumPrefixPrompts = 20
		cfg.PrefixPromptLength = prefixLen
		out[i] = forgeScenarioChild{
			VariantLabel: fmt.Sprintf("isl%d-c%d", isl, c),
			Config:       cfg,
		}
	}
	return out
}

func bimodalVariants() []forgeScenarioChild {
	const seqDist = "300|100,64|16:70;4000|500,256|64:30"
	out := make([]forgeScenarioChild, len(defaultSweep))
	for i, c := range defaultSweep {
		cfg := syntheticBase("")
		cfg.Concurrency = c
		cfg.NumRequests = rcMin(c)
		cfg.SeqDist = seqDist
		out[i] = forgeScenarioChild{
			VariantLabel: fmt.Sprintf("c%d", c),
			Config:       cfg,
		}
	}
	return out
}

func sustainedLoadVariants() []forgeScenarioChild {
	sweep := []int{50, 100, 150, 200, 250}
	out := make([]forgeScenarioChild, len(sweep))
	for i, c := range sweep {
		cfg := syntheticBase("")
		cfg.Concurrency = c
		cfg.NumRequests = c * 10
		cfg.ISL = 1500
		cfg.ISLStddev = 300
		cfg.OSL = 128
		out[i] = forgeScenarioChild{
			VariantLabel: fmt.Sprintf("c%d", c),
			Config:       cfg,
		}
	}
	return out
}

func burstRecoveryVariants() []forgeScenarioChild {
	const burstSeqDist = "300|100,64|16:50;3000|400,200|50:50"
	var out []forgeScenarioChild
	for rnd := 1; rnd <= 5; rnd++ {
		// Burst phase
		burstCfg := syntheticBase("")
		burstCfg.Concurrency = 200
		burstCfg.NumRequests = 400
		burstCfg.SeqDist = burstSeqDist
		out = append(out, forgeScenarioChild{
			VariantLabel: fmt.Sprintf("round%d-burst", rnd),
			Phase:        "burst",
			Round:        rnd,
			Config:       burstCfg,
		})

		// Probe phase
		probeCfg := syntheticBase("")
		probeCfg.Concurrency = 25
		probeCfg.NumRequests = 50
		probeCfg.ISL = 256
		probeCfg.ISLStddev = 50
		probeCfg.OSL = 64
		out = append(out, forgeScenarioChild{
			VariantLabel: fmt.Sprintf("round%d-probe", rnd),
			Phase:        "probe",
			Round:        rnd,
			Config:       probeCfg,
		})
	}
	return out
}

// mooncakeVariants is stubbed: WS-C2 (trace download + 0.80 time-dilation +
// tokenizer substitution) is out of scope for WS-C1. The registry entry shape
// is preserved so WS-C2 can wire in execution without changing the registry.
// expandForgeScenario rejects this key with a clear error.
func mooncakeVariants() []forgeScenarioChild {
	// TODO(WS-C2): implement trace-replay path (download, dilation=0.80, Qwen3-32B tokenizer).
	return nil
}

// ---------------------------------------------------------------------------
// Registry
// ---------------------------------------------------------------------------

// forgeScenarioRegistry is the ordered list of all scenario definitions.
// Mirror of benchmark_scenarios.py SCENARIO_PRESETS.
//
// Keyed access: use forgeScenarioByKey.
var forgeScenarioRegistry = []forgeScenario{
	{
		Key:           "baseline",
		Name:          "Baseline",
		Description:   "ISL 500 / OSL 128, concurrency sweep {50,100,150,200}, rc=max(c*5,20).",
		Tags:          []string{"synthetic"},
		buildVariants: baselineVariants,
	},
	{
		Key:           "high-concurrency",
		Name:          "High Concurrency",
		Description:   "Large prompts (ISL 5000-10000) / OSL 128, no prefix sharing, (concurrency,ISL) pairs (150,5000)(200,7000)(250,9000)(300,10000), rc=c*5.",
		Tags:          []string{"synthetic"},
		buildVariants: highConcurrencyVariants,
	},
	{
		Key:           "mixed-workload",
		Name:          "Mixed Workload",
		Description:   "Warmup + short + long phases per concurrency; long phase uses prefix prompts.",
		Tags:          []string{"synthetic", "multi-phase"},
		buildVariants: mixedWorkloadVariants,
	},
	{
		Key:           "multi-turn",
		Name:          "Multi-Turn",
		Description:   "Turn 1 no-prefix, turns 2-4 with growing prefix-prompt-length (500/1000/1500).",
		Tags:          []string{"synthetic", "prefix-cache"},
		buildVariants: multiTurnVariants,
	},
	{
		Key:           "prefix-cache",
		Name:          "Prefix Cache (chat)",
		Description:   "80% shared prefix on large prompts / OSL 128, 20 prefix prompts, prefix-prompt-length 4000-8000 (unique-token mean 1000-2000), (concurrency,ISL) pairs (150,5000)(200,7000)(250,9000)(300,10000).",
		Tags:          []string{"synthetic", "prefix-cache"},
		buildVariants: prefixCacheVariants,
	},
	// prefix-cache-completions: omitted from --scenario execution.
	// The FP8 tokenizer (neuralmagic/Meta-Llama-3.1-70B-Instruct-FP8) requires
	// HF_HOME=/model-store and a separate tokenizer download step that adds
	// disproportionate complexity for WS-C1. The chat variant (prefix-cache) is
	// the canonical benchmark; completions is a derivative. Add in WS-C2 or
	// a dedicated WS if the FP8/completions path is required.
	{
		Key:           "bimodal",
		Name:          "Bimodal",
		Description:   "Two-mode seq-dist (short 70% / long 30%), rc=max(c*5,20), sweep.",
		Tags:          []string{"synthetic"},
		buildVariants: bimodalVariants,
	},
	{
		Key:           "sustained-load",
		Name:          "Sustained Load",
		Description:   "ISL 1500±300 / OSL 128, rc=c*10, sweep {50,100,150,200,250}.",
		Tags:          []string{"synthetic"},
		buildVariants: sustainedLoadVariants,
	},
	{
		Key:           "burst-recovery",
		Name:          "Burst Recovery",
		Description:   "5 rounds of burst (c=200) + probe (c=25) phases.",
		Tags:          []string{"synthetic", "multi-phase"},
		buildVariants: burstRecoveryVariants,
	},
	{
		Key:           "mooncake",
		Name:          "Mooncake Trace (production)",
		Description:   "Open-loop, trace-driven Qwen3-32B run with 0.80x time-dilation. No concurrency sweep. (WS-C2 — not yet supported.)",
		Tags:          []string{"trace", "production"},
		TraceDriven:   true,
		buildVariants: mooncakeVariants,
	},
}

// forgeScenarioByKey looks up a scenario by its key.
func forgeScenarioByKey(key string) (forgeScenario, bool) {
	for _, s := range forgeScenarioRegistry {
		if s.Key == key {
			return s, true
		}
	}
	return forgeScenario{}, false
}

// expandForgeScenario expands a scenario key into its ordered list of child
// aiperf configs. The base AiperfConfig fields (Model, Tokenizer, HostHeader,
// Timeout, EndpointPath) are merged into each child AFTER the scenario's own
// base_flags and variant overrides — this matches expand_scenario's semantics
// where the caller's model/endpoint injection wins over scenario defaults.
//
// Returns an error when:
//   - the key is unknown
//   - the key is "mooncake" (trace scenarios are WS-C2; not yet supported)
func expandForgeScenario(key string, base jumphost.AiperfConfig) ([]forgeScenarioChild, error) {
	scenario, ok := forgeScenarioByKey(key)
	if !ok {
		valid := make([]string, 0, len(forgeScenarioRegistry))
		for _, s := range forgeScenarioRegistry {
			if !s.TraceDriven {
				valid = append(valid, s.Key)
			}
		}
		return nil, fmt.Errorf("unknown --scenario %q; valid keys: %v", key, valid)
	}
	if scenario.TraceDriven {
		return nil, fmt.Errorf("--scenario %q: trace scenarios are WS-C2; not yet supported", key)
	}

	children := scenario.buildVariants()

	// Merge caller base into each child: base fields win only when the child
	// leaves them zero/empty. This mirrors Python's: child_config = {
	// **preset.base_flags, **variant, url=..., endpoint=... }; if model: config["model"]=model
	for i := range children {
		cfg := &children[i].Config

		// Caller-supplied identity fields override scenario defaults.
		if base.Model != "" {
			cfg.Model = base.Model
		}
		if base.Tokenizer != "" {
			cfg.Tokenizer = base.Tokenizer
		}
		if base.HostHeader != "" {
			cfg.HostHeader = base.HostHeader
		}
		if base.EndpointPath != "" {
			cfg.EndpointPath = base.EndpointPath
		}
		if base.Timeout > 0 {
			cfg.Timeout = base.Timeout
		}
	}

	return children, nil
}

// forgeScenarioConfigJSON returns a map suitable for forge BenchmarkConfig
// registration for a given scenario.
func forgeScenarioConfigJSON(key string, model, vip string) map[string]any {
	s, ok := forgeScenarioByKey(key)
	if !ok {
		return map[string]any{"scenario_key": key}
	}
	return map[string]any{
		"scenario_key":  key,
		"scenario_name": s.Name,
		"model":         model,
		"url":           fmt.Sprintf("http://%s", vip),
		"description":   s.Description,
		"tags":          s.Tags,
	}
}
