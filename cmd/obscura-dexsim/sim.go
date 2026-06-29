package main

import (
	"container/heap"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"time"
)

// Config is the full simulator configuration assembled from flags.
type Config struct {
	RPC        string
	Seed       int64
	Steps      int           // number of book-sample steps (the macro clock)
	StepDelay  time.Duration // wall-clock pacing between sample steps (0 = as fast as possible)
	Pop        AgentPopulation
	Price      PriceConfig
	Scenario   *Scenario
	Thresholds StylizedThresholds
	ReportJSON string
	TradesCSV  string
	Quiet      bool
	// SampleSec is the simulated-seconds per macro step (drives Poisson arrivals
	// and time-weighting). The event queue runs in simulated seconds.
	SampleSec float64
	// Offline, when true, skips all RPC: the engine matches takes against an
	// in-process synthetic book so the stylized-facts harness can run without a
	// node (used by the self-check and for CI). The live mode uses real RPC fills.
	Offline bool
}

// Sim is the agent-based microstructure engine.
type Sim struct {
	cfg   Config
	rng   *rand.Rand
	cl    *client
	price *PriceEngine
	regs  *RegimeController
	mx    *Metrics

	agents []*Agent
	evq    eventQueue
	now    float64 // simulated seconds

	// offline synthetic book (only used when cfg.Offline): an efficient ("true")
	// mid that order flow walks via a small PERMANENT Kyle-lambda impact, plus a
	// TRANSIENT impact component that decays each step (Almgren temporary impact /
	// bid-ask bounce). Trade prices = efficient + transient; observed mid =
	// efficient. The transient component is what gives real fills near-zero return
	// autocorrelation (no spurious trending) while preserving vol clustering and
	// fat tails from the GARCH/Student-t efficient price.
	offMid       float64 // efficient permanent price
	offLambda    float64 // permanent impact per signed OBX
	offTransient float64 // current transient price offset (decays)
	offTau       float64 // permanent/transient split + decay knobs
	offTransImp  float64 // transient impact per signed OBX

	peakLive      int
	peakMid       float64
	lastTradeTime int64
}

// scheduledEvent is an agent firing at a simulated time (min-heap by time).
type scheduledEvent struct {
	t     float64
	agent *Agent
	// forced carries a scenario-injected one-shot action (whale block, etc.).
	forced *Action
	index  int
}

func NewSim(cfg Config) *Sim {
	rng := rand.New(rand.NewSource(cfg.Seed))
	s := &Sim{
		cfg:         cfg,
		rng:         rng,
		price:       NewPriceEngine(cfg.Price, rng),
		regs:        NewRegimeController(cfg.Scenario, rng),
		mx:          NewMetrics(),
		offMid:      cfg.Price.Start,
		offLambda:   0.000002, // PERMANENT Kyle impact per signed OBX (small, info content)
		offTransImp: 0.00004,  // TRANSIENT impact per signed OBX (liquidity cost, decays)
		offTau:      0.5,      // per-step transient decay fraction
	}
	if !cfg.Offline {
		s.cl = newClient(cfg.RPC, 10*time.Second)
	}
	s.agents = buildAgents(cfg.Pop, rng)
	// seed each agent's first event time.
	heap.Init(&s.evq)
	for _, a := range s.agents {
		a.scheduleNext(rng, 0)
		heap.Push(&s.evq, &scheduledEvent{t: a.nextAt, agent: a})
	}
	return s
}

// Run executes the simulation. stop is an optional channel; when it closes the
// run ends early (clean shutdown cancels the sim's own offers). Returns the
// report.
func (s *Sim) Run(stop <-chan struct{}) *RunReport {
	if !s.cfg.Quiet {
		fmt.Printf("dexsim: scenario=%s seed=%d steps=%d offline=%v agents=%d price=%s\n",
			s.cfg.Scenario.Name, s.cfg.Seed, s.cfg.Steps, s.cfg.Offline, len(s.agents), s.cfg.Price.Kind)
	}
	for step := 0; step < s.cfg.Steps; step++ {
		select {
		case <-stop:
			if !s.cfg.Quiet {
				fmt.Println("[shutdown] cancelling sim offers...")
			}
			s.cancelAll()
			return s.report(step)
		default:
		}
		s.macroStep(step)
		if s.cfg.StepDelay > 0 {
			time.Sleep(s.cfg.StepDelay)
		}
	}
	s.cancelAll()
	return s.report(s.cfg.Steps)
}

// macroStep advances one sample step: evolve price/regime, apply scenario
// events, drain the event queue for this step's simulated window, then sample
// the book for the time-weighted metrics.
func (s *Sim) macroStep(step int) {
	// regime + price/vol evolution.
	_, rp := s.regs.Step()
	s.price.SetDrift(rp.Drift)
	if rp.VolMult > 0 {
		s.price.SetVolMult(rp.VolMult)
	}
	s.price.Step()

	// decay the offline transient impact toward zero (liquidity replenishes).
	if s.cfg.Offline {
		s.offTransient *= (1 - s.offTau)
	}

	// scenario one-shot events scheduled for this step.
	s.applyEvents(step)

	// drain events up to the end of this step's simulated window.
	windowEnd := float64(step+1) * s.cfg.SampleSec
	for s.evq.Len() > 0 && s.evq[0].t <= windowEnd {
		ev := heap.Pop(&s.evq).(*scheduledEvent)
		s.now = ev.t
		s.fireEvent(ev)
		// reschedule the agent (unless it was a forced one-shot).
		if ev.forced == nil {
			ev.agent.scheduleNext(s.rng, s.now)
			heap.Push(&s.evq, &scheduledEvent{t: ev.agent.nextAt, agent: ev.agent})
		}
	}
	s.now = windowEnd

	// pull executed trades since last sample (live mode) and sample the book.
	bv := s.observe()
	if bv.mid > 0 && bv.mid > s.peakMid {
		s.peakMid = bv.mid
	}
	s.mx.SampleBook(bv, s.cfg.SampleSec)
	if !s.cfg.Offline {
		s.ingestTrades()
		if lc := s.cl.liveCount(); lc > s.peakLive {
			s.peakLive = lc
		}
	}
	if !s.cfg.Quiet && step%max(1, s.cfg.Steps/20) == 0 {
		fmt.Printf("[step %4d/%d] regime=%s fair=%.5f mid=%.5f vol=%.4f trades=%d\n",
			step, s.cfg.Steps, s.regs.Current(), s.price.Price(), bv.mid, s.price.Vol(), len(s.mx.tradePrices))
	}
}

// fireEvent runs one agent's decision and translates it into book actions.
func (s *Sim) fireEvent(ev *scheduledEvent) {
	a := ev.agent
	bv := s.observe()

	// perceived fair value: informed agents see the true fair with signal noise;
	// others perceive the engine fair directly (their strategies mostly ignore it).
	fair := s.price.Price()
	if _, ok := a.strat.(*InformedTrader); ok {
		// signal quality: ±0.5% gaussian noise on log fair.
		fair *= math.Exp(s.rng.NormFloat64() * 0.005)
	}

	var act Action
	if ev.forced != nil {
		act = *ev.forced
	} else {
		act = a.strat.Decide(s.rng, fair, bv, a.invOBX)
	}

	if act.CancelAll {
		s.cancelAgent(a)
		return
	}
	if act.PostQuotes {
		s.requoteAgent(a, act)
	}
	if act.Take {
		s.executeTake(a, act, bv)
	}
}

// applyEvents fires scenario one-shot events scheduled at this step.
func (s *Sim) applyEvents(step int) {
	for i := range s.cfg.Scenario.Events {
		e := &s.cfg.Scenario.Events[i]
		if e.Step != step {
			continue
		}
		switch e.Kind {
		case "news", "flash_crash":
			s.price.Shock(e.Magnitude)
			if e.Kind == "flash_crash" {
				// flash crash also withdraws MMs (they pull quotes) and floods sells.
				s.withdrawMakers(0.8)
			}
			if !s.cfg.Quiet {
				fmt.Printf("   [event step %d] %s factor=%.3f -> fair=%.5f\n", step, e.Kind, e.Magnitude, s.price.Price())
			}
		case "liquidity_crisis":
			s.withdrawMakers(e.Magnitude)
			s.price.VolScale(2.0)
			if !s.cfg.Quiet {
				fmt.Printf("   [event step %d] liquidity_crisis withdraw=%.0f%%\n", step, e.Magnitude*100)
			}
		case "whale":
			// schedule an immediate forced whale block from the first whale agent.
			for _, a := range s.agents {
				if w, ok := a.strat.(*Whale); ok {
					act := w.forceWhale(e.Magnitude, e.Buy)
					heap.Push(&s.evq, &scheduledEvent{t: s.now, agent: a, forced: &act})
					break
				}
			}
			if !s.cfg.Quiet {
				fmt.Printf("   [event step %d] whale %s %.0f OBX\n", step, buySell(e.Buy), e.Magnitude)
			}
		}
	}
}

// withdrawMakers cancels quotes for a fraction of market-maker agents (a MM
// withdrawal / liquidity crisis).
func (s *Sim) withdrawMakers(frac float64) {
	for _, a := range s.agents {
		if _, ok := a.strat.(*MarketMaker); !ok {
			continue
		}
		if s.rng.Float64() < frac {
			s.cancelAgent(a)
		}
	}
}

// ---- action execution -------------------------------------------------------

// requoteAgent cancels the agent's prior offers and posts fresh two-sided quotes.
func (s *Sim) requoteAgent(a *Agent, act Action) {
	s.cancelAgent(a)
	if s.cfg.Offline {
		return // offline mode has no maker book; takes hit the synthetic mid.
	}
	if act.AskSizeOBX > 0 && act.AskPrice > 0 {
		obx := s.cl.obxAtomic(act.AskSizeOBX)
		xno := s.cl.xnoFor(obx, act.AskPrice)
		if id, ok := s.cl.postOffer(a.secret, "OBX", "XNO", obx, xno, agentTTL()); ok {
			a.live = append(a.live, liveOffer{id: id, side: "SELL"})
		}
	}
	if act.BidSizeOBX > 0 && act.BidPrice > 0 {
		obx := s.cl.obxAtomic(act.BidSizeOBX)
		xno := s.cl.xnoFor(obx, act.BidPrice)
		if id, ok := s.cl.postOffer(a.secret, "XNO", "OBX", xno, obx, agentTTL()); ok {
			a.live = append(a.live, liveOffer{id: id, side: "BUY"})
		}
	}
}

// executeTake performs a REAL fill via /swaps/take (live) or the synthetic-book
// impact model (offline). Only BUY-OBX is settleable on the node (taker gives
// XNO, gets OBX), so SELL takes are modelled offline / via impact only; in live
// mode a sell is skipped (no wired settleable leg), which we log via fill rate.
func (s *Sim) executeTake(a *Agent, act Action, bv bookView) {
	size := act.TakeOBX
	if size <= 0 {
		return
	}
	signed := size
	if !act.TakeBuy {
		signed = -size
	}

	if s.cfg.Offline {
		// Almgren-style impact split: a small PERMANENT impact moves the efficient
		// price (information), and a larger TRANSIENT impact (liquidity cost) is
		// added to the EXECUTION price but decays over subsequent steps. Bounds keep
		// a fat-tailed whale slice from blowing the price up or negative.
		perm := clampImpact(s.offLambda * signed)
		trans := clampImpact(s.offTransImp * signed)
		pull := 0.02 * (s.price.Price() - s.offMid) // informed price discovery
		s.offMid += s.offMid*perm + pull
		if s.offMid < 1e-9 {
			s.offMid = 1e-9
		}
		s.offTransient += trans
		// execution price = efficient price * (1 + transient offset).
		px := s.offMid * (1 + s.offTransient)
		if px < 1e-9 {
			px = 1e-9
		}
		s.mx.RecordTrade(px, size, int64(s.now))
		s.mx.RecordTake(true)
		s.accountTake(a, act.TakeBuy, size, px)
		return
	}

	// LIVE: buy OBX = give XNO. Size the XNO to spend from the perceived ask.
	if !act.TakeBuy {
		// sells have no wired settleable leg on this node; count as an unfilled
		// take so fill-rate reflects one-sided settleability honestly.
		s.mx.RecordTake(false)
		return
	}
	px := bv.bestAsk
	if px <= 0 {
		px = s.price.Price()
	}
	xnoUnits := s.cl.xnoFor(s.cl.obxAtomic(size), px)
	tr, err := s.cl.take(xnoUnits, act.OrderType, a.pubHex)
	if err != nil || tr.GetOut == "0" || tr.GetOut == "" {
		s.mx.RecordTake(false)
		return
	}
	s.mx.RecordTake(true)
	// account using the OBX actually received and XNO reserved.
	obxOut := parseUnits(tr.GetOut, s.cl.decOBX)
	xnoIn := parseUnits(tr.Reserved, s.cl.decXNO)
	if obxOut > 0 {
		fillPx := xnoIn / obxOut
		s.accountTake(a, true, obxOut, fillPx)
	}
}

// accountTake updates an agent's inventory/cash/realized P&L for a fill.
func (s *Sim) accountTake(a *Agent, buy bool, obx, px float64) {
	a.trades++
	if buy {
		a.invOBX += obx
		a.cashXNO -= obx * px
	} else {
		a.invOBX -= obx
		a.cashXNO += obx * px
	}
}

// ---- book observation -------------------------------------------------------

func (s *Sim) observe() bookView {
	if s.cfg.Offline {
		// synthetic top-of-book around offMid with a small spread + fixed depth.
		half := s.offMid * 0.002
		return bookView{
			bestBid: s.offMid - half, bestAsk: s.offMid + half, mid: s.offMid,
			askDepthXNO: 1e6, bidDepthXNO: 1e6, hasBid: true, hasAsk: true,
		}
	}
	return s.cl.observe()
}

// ingestTrades pulls newly executed fills from the node tape into the metrics so
// the stylized-facts harness sees REAL fills. We track the newest seen time.
func (s *Sim) ingestTrades() {
	tr := s.cl.trades("XNO/OBX", 200)
	for _, t := range tr.Trades {
		if t.Time <= s.lastTradeTime {
			continue
		}
		var price, give, get float64
		fmt.Sscanf(t.Price, "%g", &price)
		fmt.Sscanf(t.Give, "%g", &give)
		fmt.Sscanf(t.Get, "%g", &get)
		// price is raw get/give (OBX-units per XNO-unit for XNO/OBX). Convert to
		// XNO/OBX = 1/price, and OBX size = get / 10^decOBX.
		var xnoPerObx, obxSize float64
		if price > 0 {
			xnoPerObx = 1 / price
		}
		obxSize = get / math.Pow10(s.cl.decOBX)
		s.mx.RecordTrade(xnoPerObx, obxSize, t.Time)
		if t.Time > s.lastTradeTime {
			s.lastTradeTime = t.Time
		}
	}
}

// ---- shutdown ---------------------------------------------------------------

func (s *Sim) cancelAgent(a *Agent) {
	if s.cfg.Offline {
		a.live = nil
		return
	}
	for _, lo := range a.live {
		_ = s.cl.cancel(a.secret, lo.id)
	}
	a.live = nil
}

func (s *Sim) cancelAll() {
	if s.cfg.Offline {
		return
	}
	var wg sync.WaitGroup
	for _, a := range s.agents {
		for _, lo := range a.live {
			wg.Add(1)
			go func(a *Agent, id [32]byte) {
				defer wg.Done()
				_ = s.cl.cancel(a.secret, id)
			}(a, lo.id)
		}
		a.live = nil
	}
	wg.Wait()
}

// ---- report -----------------------------------------------------------------

func (s *Sim) report(steps int) *RunReport {
	returns := logReturns(s.mx.tradePrices)
	if len(returns) < 30 {
		// fall back to mid returns when the tape is thin (e.g. offline / short run).
		returns = logReturns(s.mx.mids)
	}
	styl := ValidateStylizedFacts(returns, s.cfg.Thresholds)

	agents := make([]AgentStat, 0, len(s.agents))
	finalPx := s.observe().mid
	for _, a := range s.agents {
		mark := a.cashXNO + a.invOBX*finalPx
		agents = append(agents, AgentStat{
			ID: a.id, Strategy: a.strat.Name(),
			Inventory: a.invOBX, CashXNO: a.cashXNO,
			RealizedP: a.realized, MarkPnL: mark, Trades: a.trades,
		})
	}

	return &RunReport{
		Seed: s.cfg.Seed, Scenario: s.cfg.Scenario.Name, Steps: steps,
		Trades: len(s.mx.tradePrices), Volume: s.mx.volume(), VWAP: s.mx.vwap(),
		RealizedVol: s.mx.realizedVol(), TWSpread: s.mx.twSpread(), TWDepth: s.mx.twDepth(),
		FillRate: s.mx.fillRate(), FairFinal: s.price.Price(), MidFinal: finalPx,
		Stylized: styl, Agents: agents, LiveOffersHi: s.peakLive,
	}
}

// clampImpact bounds a per-trade price-impact fraction to [-25%,+25%].
func clampImpact(x float64) float64 {
	if x > 0.25 {
		return 0.25
	}
	if x < -0.25 {
		return -0.25
	}
	return x
}

func buySell(buy bool) string {
	if buy {
		return "BUY"
	}
	return "SELL"
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ---- event queue (min-heap by simulated time) -------------------------------

type eventQueue []*scheduledEvent

func (q eventQueue) Len() int           { return len(q) }
func (q eventQueue) Less(i, j int) bool { return q[i].t < q[j].t }
func (q eventQueue) Swap(i, j int)      { q[i], q[j] = q[j], q[i]; q[i].index = i; q[j].index = j }
func (q *eventQueue) Push(x any)        { *q = append(*q, x.(*scheduledEvent)) }
func (q *eventQueue) Pop() any {
	old := *q
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	*q = old[:n-1]
	return it
}
