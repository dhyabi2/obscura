package swapd

import "fmt"

// NanoRPCPreset is a known-working public Nano RPC offered as a SELECTABLE default in the
// operator config (per the maintainer's request for a built-in pick-list). This does not
// reintroduce hardcoding into the swap LOGIC: the protocol never reaches for a URL on its
// own — an operator must still explicitly select a preset (by name) or pass a custom URL.
// The presets are just a convenience pick-list, each labeled with its real capabilities so
// an operator knows whether it can publish blocks (process) and generate work.
type NanoRPCPreset struct {
	Name       string // short selector, e.g. "rainstorm"
	URL        string // RPC endpoint
	WorkURL    string // where to send work_generate if this node can't do it (empty = same as URL)
	CanWork    bool   // node answers work_generate
	CanProcess bool   // node accepts process (publishes blocks)
	Note       string
}

// PublicNanoRPCs is the built-in selectbox of working public Nano RPCs (verified live
// against each endpoint). "rainstorm" is the only one that does work_generate, so the
// others fall back to it for work while using their own endpoint for reads + process.
var PublicNanoRPCs = []NanoRPCPreset{
	{
		Name: "rainstorm", URL: "https://rainstorm.city/api",
		CanWork: true, CanProcess: true,
		Note: "RainstormCity — FULL: reads + process + work_generate (recommended for swaps)",
	},
	{
		Name: "somenano", URL: "https://node.somenano.com/proxy", WorkURL: "https://rainstorm.city/api",
		CanWork: false, CanProcess: true,
		Note: "SomeNano — reads + process; no work_generate (uses rainstorm for work)",
	},
	{
		Name: "nanoto", URL: "https://rpc.nano.to", WorkURL: "https://rainstorm.city/api",
		CanWork: false, CanProcess: true,
		Note: "Nano.to — reads + process; no work_generate (uses rainstorm for work)",
	},
}

// DefaultNanoPreset is the preset selected when an operator asks for a public RPC without
// naming one. It is the only fully-capable public endpoint.
const DefaultNanoPreset = "rainstorm"

// ResolveNanoSelector turns a selector — a preset NAME ("rainstorm"/"somenano"/"nanoto"),
// the special value "public"/"auto" (→ DefaultNanoPreset), or a literal URL — into a base
// NanoRPCConfig (URL + WorkURL). Auth/wallet/source are layered on by the caller. The bool
// reports whether the selector matched a known preset (vs. a custom URL).
func ResolveNanoSelector(sel string) (cfg NanoRPCConfig, preset bool) {
	if sel == "public" || sel == "auto" || sel == "default" {
		sel = DefaultNanoPreset
	}
	for _, p := range PublicNanoRPCs {
		if p.Name == sel {
			work := p.WorkURL
			if work == "" {
				work = p.URL
			}
			return NanoRPCConfig{URL: p.URL, WorkURL: work}, true
		}
	}
	// not a preset name → treat as a custom URL (operator-provided, zero hardcoding).
	return NanoRPCConfig{URL: sel}, false
}

// NanoPresetList renders the built-in selectbox for help text / logs.
func NanoPresetList() string {
	s := "available --nano-rpc presets (or pass a full URL):\n"
	for _, p := range PublicNanoRPCs {
		s += fmt.Sprintf("  %-10s %s\n             %s\n", p.Name, p.URL, p.Note)
	}
	s += "  public     (alias for '" + DefaultNanoPreset + "')\n"
	return s
}
