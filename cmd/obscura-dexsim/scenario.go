package main

import (
	"encoding/json"
	"math/rand"
	"os"
)

// Regime is a Markov market state. Each regime sets a drift bias and a vol
// multiplier applied to the price engine; transitions follow a transition
// matrix sampled once per step.
type Regime int

const (
	RegimeBull Regime = iota
	RegimeBear
	RegimeRange
)

func (r Regime) String() string {
	switch r {
	case RegimeBull:
		return "bull"
	case RegimeBear:
		return "bear"
	default:
		return "range"
	}
}

// RegimeParams maps a regime to its drift and vol scaling.
type RegimeParams struct {
	Drift   float64 `json:"drift"`    // per-step log-drift
	VolMult float64 `json:"vol_mult"` // multiplies conditional variance each step
}

// ScenarioEvent is a one-shot scheduled disturbance at a given step.
type ScenarioEvent struct {
	Step int    `json:"step"`
	Kind string `json:"kind"` // "news" | "flash_crash" | "liquidity_crisis" | "whale"
	// Magnitude meaning depends on Kind:
	//   news/flash_crash: multiplicative price factor (0.8 = -20%).
	//   liquidity_crisis: fraction of makers that withdraw (0..1).
	//   whale: OBX block size to dump/lift.
	Magnitude float64 `json:"magnitude"`
	// Buy is the direction for a whale event (true = buy/lift, false = sell/dump).
	Buy bool `json:"buy"`
}

// Scenario is the full reproducible scenario configuration. It can be loaded
// from JSON or constructed by name from a preset.
type Scenario struct {
	Name string `json:"name"`

	// Regime switching: transition matrix [from][to] (rows sum ~1) + per-regime
	// drift/vol. Empty/zero matrix disables regime switching (single regime).
	Regimes     map[string]RegimeParams `json:"regimes"`
	Transition  [][]float64             `json:"transition"` // 3x3 bull/bear/range
	StartRegime string                  `json:"start_regime"`

	Events []ScenarioEvent `json:"events"`
}

// LoadScenario reads a scenario JSON file. An empty path yields the calm preset.
func LoadScenario(path string) (*Scenario, error) {
	if path == "" {
		return PresetScenario("calm"), nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Scenario
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	if s.Name == "" {
		s.Name = "custom"
	}
	return &s, nil
}

// PresetScenario returns a built-in scenario by name. Known names: calm,
// regime_switch, flash_crash, liquidity_crisis, news, whale_day.
func PresetScenario(name string) *Scenario {
	regimes := map[string]RegimeParams{
		"bull":  {Drift: 0.0008, VolMult: 1.0},
		"bear":  {Drift: -0.0009, VolMult: 1.4},
		"range": {Drift: 0.0, VolMult: 0.8},
	}
	// Persistent regimes: high self-transition probability.
	trans := [][]float64{
		{0.97, 0.02, 0.01}, // from bull
		{0.03, 0.95, 0.02}, // from bear
		{0.02, 0.02, 0.96}, // from range
	}
	switch name {
	case "regime_switch":
		return &Scenario{Name: name, Regimes: regimes, Transition: trans, StartRegime: "range"}
	case "flash_crash":
		return &Scenario{Name: name, Regimes: regimes, Transition: trans, StartRegime: "bull",
			Events: []ScenarioEvent{{Step: 0, Kind: "flash_crash", Magnitude: 0.75}}}
	case "liquidity_crisis":
		return &Scenario{Name: name, Regimes: regimes, Transition: trans, StartRegime: "range",
			Events: []ScenarioEvent{{Step: 0, Kind: "liquidity_crisis", Magnitude: 0.7}}}
	case "news":
		return &Scenario{Name: name, Regimes: regimes, Transition: trans, StartRegime: "range",
			Events: []ScenarioEvent{{Step: 0, Kind: "news", Magnitude: 1.15}}}
	case "whale_day":
		return &Scenario{Name: name, Regimes: regimes, Transition: trans, StartRegime: "range",
			Events: []ScenarioEvent{{Step: 0, Kind: "whale", Magnitude: 5000, Buy: true}}}
	default: // calm
		return &Scenario{Name: "calm", StartRegime: "range",
			Regimes: map[string]RegimeParams{"range": {Drift: 0, VolMult: 1.0}}}
	}
}

// regimeIndex maps a name to an index for the transition matrix.
func regimeIndex(name string) Regime {
	switch name {
	case "bull":
		return RegimeBull
	case "bear":
		return RegimeBear
	default:
		return RegimeRange
	}
}

// RegimeController drives the Markov regime walk and exposes the active params.
type RegimeController struct {
	sc      *Scenario
	cur     Regime
	enabled bool
	rng     *rand.Rand
}

func NewRegimeController(sc *Scenario, rng *rand.Rand) *RegimeController {
	rc := &RegimeController{sc: sc, rng: rng, cur: regimeIndex(sc.StartRegime)}
	rc.enabled = len(sc.Transition) == 3 && len(sc.Regimes) > 1
	return rc
}

// Step advances the regime via the transition matrix (if enabled) and returns
// the (possibly new) regime plus its params.
func (rc *RegimeController) Step() (Regime, RegimeParams) {
	if rc.enabled {
		row := rc.sc.Transition[int(rc.cur)]
		u := rc.rng.Float64()
		cum := 0.0
		for j, p := range row {
			cum += p
			if u <= cum {
				rc.cur = Regime(j)
				break
			}
		}
	}
	return rc.cur, rc.params()
}

func (rc *RegimeController) params() RegimeParams {
	if p, ok := rc.sc.Regimes[rc.cur.String()]; ok {
		return p
	}
	return RegimeParams{Drift: 0, VolMult: 1.0}
}

// Current returns the active regime.
func (rc *RegimeController) Current() Regime { return rc.cur }
