// Command obscura-dexsim is an AGENT-BASED MARKET MICROSTRUCTURE SIMULATOR for
// the Obscura swap order book. It replaces the old random-walk-with-cancel-trades
// toy with a population of heterogeneous trading agents whose order flow drives a
// REAL order book over the node's JSON-RPC.
//
// What it does, honestly:
//   - Many persistent agents (MarketMaker, InformedTrader, NoiseTrader,
//     MomentumTrader, MeanReversionTrader, Arbitrageur, Whale), each with
//     heterogeneous parameters drawn from distributions at construction and an
//     independent Poisson/Hawkes arrival process on a shared event queue.
//   - A selectable price/vol engine (GBM / OU / jump-diffusion) with GARCH(1,1)
//     volatility clustering and Student-t (fat-tailed) innovations, anchoring the
//     informed agents' fair value. The LIVE book price emerges from order flow.
//   - REAL fills: takers cross the live book via /swaps/take (which reserves +
//     commits real trades to the tape — these are genuine fills, not cancels).
//     Makers post signed two-sided quotes via /offer and cancel via /offer/cancel.
//   - Scenarios (news shock, flash crash, liquidity crisis, Markov regime
//     switching, whale events) via presets or a JSON file, reproducible from a
//     seed.
//   - A structured run report (JSON + optional trade-tape CSV) with realized vol,
//     VWAP, volume, fill rate, time-weighted spread/depth, per-agent P&L, AND a
//     stylized-facts validation gate (return ACF≈0, vol-clustering ACF slow decay,
//     return kurtosis>3) that is the acceptance proof the engine is realistic.
//
// Offline mode (-offline) runs the whole engine against an in-process synthetic
// book with a Kyle's-lambda price-impact model, so the math + stylized facts can
// be exercised WITHOUT a node (also how the _test.go self-check runs). Live mode
// needs a running node at -rpc.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	var (
		rpcURL  = flag.String("rpc", "http://127.0.0.1:18091", "node JSON-RPC base URL")
		seed    = flag.Int64("seed", 1, "PRNG seed (reproducible runs)")
		steps   = flag.Int("steps", 2000, "number of sample steps (the macro clock)")
		sampleS = flag.Float64("sample-sec", 1.0, "simulated seconds per step (Poisson arrivals + time-weighting)")
		delay   = flag.Duration("step-delay", 0, "wall-clock pacing per step (0 = as fast as possible)")
		startRt = flag.Float64("start-rate", 1.0, "initial fair value (XNO per OBX)")
		offline = flag.Bool("offline", false, "run against an in-process synthetic book (no node)")
		quiet   = flag.Bool("quiet", false, "suppress progress logging")

		procKind = flag.String("process", "gbm", "fair-value process: gbm|ou|jump")
		drift    = flag.Float64("drift", 0.0, "per-step log-drift")
		garchA   = flag.Float64("garch-alpha", 0.09, "GARCH(1,1) alpha (shock weight)")
		garchB   = flag.Float64("garch-beta", 0.88, "GARCH(1,1) beta (persistence)")
		garchO   = flag.Float64("garch-omega", 2e-7, "GARCH(1,1) omega (base variance)")
		nu       = flag.Float64("nu", 4.0, "Student-t degrees of freedom (fat tails; large->Gaussian)")

		scenName = flag.String("scenario", "calm", "preset: calm|regime_switch|flash_crash|liquidity_crisis|news|whale_day")
		scenFile = flag.String("scenario-file", "", "path to a scenario JSON file (overrides -scenario)")

		// population mix
		nMM  = flag.Int("mm", 6, "market makers")
		nInf = flag.Int("informed", 4, "informed traders")
		nNoi = flag.Int("noise", 10, "noise traders")
		nMom = flag.Int("momentum", 3, "momentum traders")
		nMR  = flag.Int("meanrev", 3, "mean-reversion traders")
		nArb = flag.Int("arb", 2, "arbitrageurs")
		nWh  = flag.Int("whale", 1, "whales")

		reportJSON = flag.String("report", "", "write the JSON run report here (default: stdout)")
		tradesCSV  = flag.String("trades-csv", "", "write the executed-trade tape CSV here")
	)
	flag.Parse()

	scen, err := LoadScenario(*scenFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "scenario load error:", err)
		os.Exit(1)
	}
	if *scenFile == "" {
		scen = PresetScenario(*scenName)
	}

	pc := DefaultPriceConfig(*startRt)
	pc.Kind = ProcessKind(*procKind)
	pc.Drift = *drift
	pc.Alpha = *garchA
	pc.Beta = *garchB
	pc.Omega = *garchO
	pc.Nu = *nu

	cfg := Config{
		RPC: *rpcURL, Seed: *seed, Steps: *steps, StepDelay: *delay,
		SampleSec: *sampleS, Offline: *offline, Quiet: *quiet,
		Pop: AgentPopulation{
			MarketMaker: *nMM, Informed: *nInf, Noise: *nNoi, Momentum: *nMom,
			MeanReversion: *nMR, Arbitrageur: *nArb, Whale: *nWh,
		},
		Price: pc, Scenario: scen, Thresholds: DefaultThresholds(),
		ReportJSON: *reportJSON, TradesCSV: *tradesCSV,
	}

	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		close(stop)
	}()

	sim := NewSim(cfg)
	report := sim.Run(stop)

	if err := report.WriteJSON(cfg.ReportJSON); err != nil {
		fmt.Fprintln(os.Stderr, "report write error:", err)
	}
	if err := sim.mx.WriteTradesCSV(cfg.TradesCSV); err != nil {
		fmt.Fprintln(os.Stderr, "trades csv error:", err)
	}

	// concise verdict to stderr so it's visible even when -report writes a file.
	v := "FAIL"
	if report.Stylized.Pass {
		v = "PASS"
	}
	fmt.Fprintf(os.Stderr, "\nstylized-facts gate: %s  (returns=%d kurtosis=%.2f absACF1=%.3f retACF1=%.3f)\n",
		v, report.Stylized.NReturns, report.Stylized.Returns.Kurtosis,
		report.Stylized.Returns.AbsACF1, report.Stylized.Returns.ReturnACF)
}
