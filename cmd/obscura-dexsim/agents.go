package main

import (
	"encoding/hex"
	"math"
	"math/rand"
	"time"

	"filippo.io/edwards25519"

	"obscura/pkg/commit"
)

// Action is what an agent decides to do when its event fires. The engine
// translates Actions into REAL RPC calls: maker quotes become signed /offer
// posts (and /offer/cancel for the prior quotes); taker orders become /swaps/take
// market/IOC fills that walk the live book.
type Action struct {
	// Maker quotes (two-sided). When PostQuotes is set the engine cancels the
	// agent's prior offers and posts fresh BUY/SELL OBX offers at these prices and
	// sizes. Sizes are in human OBX; prices in XNO/OBX.
	PostQuotes bool
	AskPrice   float64 // SELL-OBX price (give OBX, get XNO)
	BidPrice   float64 // BUY-OBX price  (give XNO, get OBX)
	AskSizeOBX float64
	BidSizeOBX float64

	// Cancel-all without reposting (e.g. MM withdrawal in a liquidity crisis).
	CancelAll bool

	// Taker order: cross the book. TakeOBX>0 means buy OBX (give XNO), TakeOBX<0
	// means sell OBX (give XNO is wrong — sells aren't wired on this node, so the
	// engine treats sells as "no settleable leg" and skips them, see engine). Size
	// is |TakeOBX| human OBX.
	Take      bool
	TakeBuy   bool    // true = buy OBX, false = sell OBX
	TakeOBX   float64 // human OBX to fill
	OrderType string  // "market" | "ioc" | "fok"
}

// Strategy is the behavioural core of an agent. Decide is called when the
// agent's Poisson/Hawkes event fires; it returns the action to take given the
// current observed book and the (hidden) fair value the agent perceives.
type Strategy interface {
	Name() string
	// Decide returns the action for this firing. `fair` is the agent's PERCEIVED
	// fair value (informed agents see it with noise; others may ignore it). `bv`
	// is the live top-of-book. `inv` is the agent's current OBX inventory.
	Decide(rng *rand.Rand, fair float64, bv bookView, inv float64) Action
}

// Agent couples a persistent identity (keypair), heterogeneous parameters, an
// arrival process, accounting (inventory + cash + realized P&L), and a strategy.
type Agent struct {
	id     int
	secret *edwards25519.Scalar
	pubHex string

	strat Strategy

	// arrival process: Poisson base rate (events/sec) with optional Hawkes
	// self-excitation: rate = baseRate + Σ jump*exp(-decay*(t-t_i)). nextAt is the
	// scheduled next event time (sim seconds).
	baseRate float64
	hawkesK  float64 // self-excitation jump per own event
	hawkesD  float64 // exponential decay rate
	exc      float64 // current excitation level
	lastT    float64 // last event time (for decay)
	nextAt   float64

	// latency (sim seconds) between deciding and the action landing — informed and
	// arb agents act on slightly stale info.
	latency float64

	// accounting (offer-unit human terms): inventory in OBX, cash in XNO.
	invOBX   float64
	cashXNO  float64
	realized float64 // realized P&L in XNO
	trades   int

	// live maker offers this agent must cancel before requoting.
	live []liveOffer
}

type liveOffer struct {
	id   [32]byte
	side string // "SELL" or "BUY"
}

func (a *Agent) PubHex() string { return a.pubHex }

// dropLive removes one offer id from the agent's live set.
func (a *Agent) dropLive(id [32]byte) {
	out := a.live[:0]
	for _, lo := range a.live {
		if lo.id != id {
			out = append(out, lo)
		}
	}
	a.live = out
}

// updateExcitation decays the Hawkes excitation to time t and returns the
// current intensity. Pure function of state, used by scheduleNext.
func (a *Agent) intensityAt(t float64) float64 {
	if a.hawkesK <= 0 {
		return a.baseRate
	}
	dt := t - a.lastT
	if dt < 0 {
		dt = 0
	}
	return a.baseRate + a.exc*math.Exp(-a.hawkesD*dt)
}

// scheduleNext draws the next event time from the (current) arrival intensity
// and, for Hawkes agents, records the self-excitation jump.
func (a *Agent) scheduleNext(rng *rand.Rand, now float64) {
	rate := a.intensityAt(now)
	if rate <= 0 {
		rate = 1e-6
	}
	a.nextAt = now + expRand(rng, rate)
	if a.hawkesK > 0 {
		// decay accumulated excitation to now, then add this event's jump.
		dt := now - a.lastT
		if dt < 0 {
			dt = 0
		}
		a.exc = a.exc*math.Exp(-a.hawkesD*dt) + a.hawkesK
		a.lastT = now
	}
}

// ---- agent construction -----------------------------------------------------

// AgentPopulation is the population mix: weights per strategy. The engine draws
// each agent's strategy by weight and its params from distributions.
type AgentPopulation struct {
	MarketMaker   int
	Informed      int
	Noise         int
	Momentum      int
	MeanReversion int
	Arbitrageur   int
	Whale         int
}

// DefaultPopulation is a realistic mix dominated by MMs + noise with a sprinkle
// of informed/momentum/mean-reversion and rare whales.
func DefaultPopulation() AgentPopulation {
	return AgentPopulation{
		MarketMaker:   6,
		Informed:      4,
		Noise:         10,
		Momentum:      3,
		MeanReversion: 3,
		Arbitrageur:   2,
		Whale:         1,
	}
}

// buildAgents constructs the full agent set with heterogeneous, persistent
// per-agent parameters drawn at construction. Deterministic given rng.
func buildAgents(pop AgentPopulation, rng *rand.Rand) []*Agent {
	var agents []*Agent
	id := 0
	add := func(n int, make func(int) Strategy, mkRates func() (float64, float64, float64, float64)) {
		for i := 0; i < n; i++ {
			base, hk, hd, lat := mkRates()
			a := newAgent(id, make(id), base, hk, hd, lat)
			agents = append(agents, a)
			id++
		}
	}

	// Each strategy class draws its own arrival/latency profile.
	mmRates := func() (float64, float64, float64, float64) {
		return 0.25 + rng.Float64()*0.25, 0, 0, 0.05 + rng.Float64()*0.1 // steady, low latency
	}
	infRates := func() (float64, float64, float64, float64) {
		return 0.08 + rng.Float64()*0.1, 0.15, 0.5, 0.3 + rng.Float64()*0.7 // higher latency
	}
	noiseRates := func() (float64, float64, float64, float64) {
		return 0.15 + rng.Float64()*0.25, 0, 0, 0.1 + rng.Float64()*0.3
	}
	momRates := func() (float64, float64, float64, float64) {
		return 0.1 + rng.Float64()*0.1, 0.25, 0.4, 0.2 + rng.Float64()*0.4 // Hawkes clustering
	}
	mrRates := func() (float64, float64, float64, float64) {
		return 0.1 + rng.Float64()*0.1, 0.1, 0.6, 0.2 + rng.Float64()*0.4
	}
	arbRates := func() (float64, float64, float64, float64) {
		return 0.5 + rng.Float64()*0.5, 0, 0, 0.02 + rng.Float64()*0.05 // fast, frequent
	}
	whaleRates := func() (float64, float64, float64, float64) {
		return 0.005 + rng.Float64()*0.01, 0, 0, 0.2 // rare
	}

	add(pop.MarketMaker, func(int) Strategy { return newMarketMaker(rng) }, mmRates)
	add(pop.Informed, func(int) Strategy { return newInformed(rng) }, infRates)
	add(pop.Noise, func(int) Strategy { return newNoise(rng) }, noiseRates)
	add(pop.Momentum, func(int) Strategy { return newMomentum(rng) }, momRates)
	add(pop.MeanReversion, func(int) Strategy { return newMeanReversion(rng) }, mrRates)
	add(pop.Arbitrageur, func(int) Strategy { return newArbitrageur(rng) }, arbRates)
	add(pop.Whale, func(int) Strategy { return newWhale(rng) }, whaleRates)
	return agents
}

func newAgent(id int, strat Strategy, base, hk, hd, lat float64) *Agent {
	// Deterministic per-agent secret so the sim owns every key it posts under and
	// can always cancel its own offers (and re-derive after a restart).
	sec := commit.HashToScalar([]byte("obscura/dexsim/agent/v2"), []byte{byte(id), byte(id >> 8)})
	pub := new(edwards25519.Point).ScalarBaseMult(sec).Bytes()
	return &Agent{
		id:       id,
		secret:   sec,
		pubHex:   hex.EncodeToString(pub),
		strat:    strat,
		baseRate: base,
		hawkesK:  hk,
		hawkesD:  hd,
		latency:  lat,
	}
}

// ttl gives each agent's offers a short TTL so cancelled/stale quotes expire.
func agentTTL() time.Duration { return 90 * time.Second }

// ============================================================================
// Strategies
// ============================================================================

// --- MarketMaker: Avellaneda-Stoikov inventory-skewed two-sided quotes -------
//
// The A-S model sets a RESERVATION PRICE that skews away from inventory:
//
//	r = s - q * gamma * sigma^2 * (T - t)
//
// where s = mid/fair, q = inventory, gamma = risk aversion, sigma = vol. The
// optimal half-spread is
//
//	delta = gamma*sigma^2*(T-t) + (2/gamma)*ln(1 + gamma/kappa)
//
// We quote bid = r - delta, ask = r + delta. A long inventory (q>0) lowers r,
// making the agent sell more aggressively (lower ask) and buy less — exactly the
// inventory-control behaviour real MMs exhibit.
type MarketMaker struct {
	gamma    float64 // risk aversion
	kappa    float64 // order-book liquidity / fill intensity
	sigma    float64 // assumed vol
	horizon  float64 // (T-t) inventory-risk horizon
	invLimit float64 // max |inventory| OBX before it stops quoting one side
	baseSize float64 // OBX per quote
}

func newMarketMaker(rng *rand.Rand) *MarketMaker {
	return &MarketMaker{
		gamma:    0.05 + rng.Float64()*0.25,
		kappa:    1.0 + rng.Float64()*3.0,
		sigma:    0.01 + rng.Float64()*0.02,
		horizon:  1.0,
		invLimit: 200 + pareto(rng, 200, 1.5), // heterogeneous capital
		baseSize: 5 + rng.Float64()*15,
	}
}

func (m *MarketMaker) Name() string { return "MarketMaker" }

func (m *MarketMaker) Decide(rng *rand.Rand, fair float64, bv bookView, inv float64) Action {
	s := fair
	if bv.mid > 0 {
		s = 0.5*bv.mid + 0.5*fair // anchor to both observed mid and own fair
	}
	sig2 := m.sigma * m.sigma
	// reservation price skewed by inventory
	r := s - inv*m.gamma*sig2*m.horizon
	// optimal half-spread (A-S)
	delta := m.gamma*sig2*m.horizon + (2.0/m.gamma)*math.Log(1+m.gamma/m.kappa)
	// scale delta to a price fraction of s (A-S delta is in price units already,
	// but our sigma is a return-vol so delta is small; floor to a sane spread).
	half := delta
	if half < s*0.002 {
		half = s * 0.002
	}
	ask := r + half
	bid := r - half
	if bid < s*0.0001 {
		bid = s * 0.0001
	}

	a := Action{PostQuotes: true, AskPrice: ask, BidPrice: bid,
		AskSizeOBX: m.baseSize, BidSizeOBX: m.baseSize}
	// inventory limits: stop adding to a side that would breach the limit.
	if inv >= m.invLimit {
		a.BidSizeOBX = 0 // already too long; don't buy more
	}
	if inv <= -m.invLimit {
		a.AskSizeOBX = 0 // already too short; don't sell more
	}
	return a
}

// --- InformedTrader: trades toward a private fair value (with signal noise) ---
type InformedTrader struct {
	edge    float64 // min mispricing fraction to act on
	aggr    float64 // size as fraction of perceived edge
	maxSize float64
}

func newInformed(rng *rand.Rand) *InformedTrader {
	return &InformedTrader{
		edge:    0.003 + rng.Float64()*0.01,
		aggr:    50 + rng.Float64()*150,
		maxSize: 20 + pareto(rng, 20, 1.8),
	}
}

func (it *InformedTrader) Name() string { return "InformedTrader" }

func (it *InformedTrader) Decide(rng *rand.Rand, fair float64, bv bookView, inv float64) Action {
	if bv.mid <= 0 {
		return Action{}
	}
	// fair already carries this agent's signal noise (applied by the engine).
	mis := (fair - bv.mid) / bv.mid
	if math.Abs(mis) < it.edge {
		return Action{}
	}
	size := math.Min(it.aggr*math.Abs(mis), it.maxSize)
	if size <= 0 {
		return Action{}
	}
	return Action{Take: true, TakeBuy: mis > 0, TakeOBX: size, OrderType: "ioc"}
}

// --- NoiseTrader: zero-intelligence random buy/sell ---------------------------
type NoiseTrader struct {
	mu, sigma float64 // lognormal size params
}

func newNoise(rng *rand.Rand) *NoiseTrader {
	return &NoiseTrader{mu: 0.5 + rng.Float64(), sigma: 0.8 + rng.Float64()*0.6}
}

func (n *NoiseTrader) Name() string { return "NoiseTrader" }

func (n *NoiseTrader) Decide(rng *rand.Rand, fair float64, bv bookView, inv float64) Action {
	size := logNormal(rng, n.mu, n.sigma)
	if size <= 0 {
		return Action{}
	}
	return Action{Take: true, TakeBuy: rng.Float64() < 0.5, TakeOBX: size, OrderType: "ioc"}
}

// --- MomentumTrader: EMA-crossover / breakout follower ------------------------
type MomentumTrader struct {
	fast, slow float64 // EMA spans (alpha derived)
	emaF, emaS float64
	init       bool
	thresh     float64
	size       float64
}

func newMomentum(rng *rand.Rand) *MomentumTrader {
	return &MomentumTrader{
		fast:   3 + rng.Float64()*5,
		slow:   15 + rng.Float64()*25,
		thresh: 0.001 + rng.Float64()*0.003,
		size:   5 + rng.Float64()*20,
	}
}

func (mt *MomentumTrader) Name() string { return "MomentumTrader" }

func (mt *MomentumTrader) Decide(rng *rand.Rand, fair float64, bv bookView, inv float64) Action {
	if bv.mid <= 0 {
		return Action{}
	}
	af := 2.0 / (mt.fast + 1)
	as := 2.0 / (mt.slow + 1)
	if !mt.init {
		mt.emaF, mt.emaS, mt.init = bv.mid, bv.mid, true
		return Action{}
	}
	mt.emaF += af * (bv.mid - mt.emaF)
	mt.emaS += as * (bv.mid - mt.emaS)
	gap := (mt.emaF - mt.emaS) / mt.emaS
	if math.Abs(gap) < mt.thresh {
		return Action{}
	}
	return Action{Take: true, TakeBuy: gap > 0, TakeOBX: mt.size, OrderType: "ioc"}
}

// --- MeanReversionTrader: z-score vs a moving average -------------------------
type MeanReversionTrader struct {
	span   float64
	emaM   float64 // running mean
	emaV   float64 // running variance (EW)
	init   bool
	zEntry float64
	size   float64
}

func newMeanReversion(rng *rand.Rand) *MeanReversionTrader {
	return &MeanReversionTrader{
		span:   20 + rng.Float64()*30,
		zEntry: 1.5 + rng.Float64(),
		size:   5 + rng.Float64()*15,
	}
}

func (mr *MeanReversionTrader) Name() string { return "MeanReversionTrader" }

func (mr *MeanReversionTrader) Decide(rng *rand.Rand, fair float64, bv bookView, inv float64) Action {
	if bv.mid <= 0 {
		return Action{}
	}
	a := 2.0 / (mr.span + 1)
	if !mr.init {
		mr.emaM, mr.emaV, mr.init = bv.mid, 0, true
		return Action{}
	}
	d := bv.mid - mr.emaM
	mr.emaM += a * d
	mr.emaV = (1 - a) * (mr.emaV + a*d*d)
	sd := math.Sqrt(mr.emaV)
	if sd <= 0 {
		return Action{}
	}
	z := (bv.mid - mr.emaM) / sd
	if math.Abs(z) < mr.zEntry {
		return Action{}
	}
	// price above mean (z>0) => sell (revert down); below => buy.
	return Action{Take: true, TakeBuy: z < 0, TakeOBX: mr.size, OrderType: "ioc"}
}

// --- Arbitrageur: acts only when bid crosses ask (locked/crossed book) --------
type Arbitrageur struct {
	size float64
}

func newArbitrageur(rng *rand.Rand) *Arbitrageur {
	return &Arbitrageur{size: 10 + rng.Float64()*30}
}

func (ar *Arbitrageur) Name() string { return "Arbitrageur" }

func (ar *Arbitrageur) Decide(rng *rand.Rand, fair float64, bv bookView, inv float64) Action {
	// crossed book: best bid >= best ask => risk-free lift of the cheap ask.
	if bv.hasBid && bv.hasAsk && bv.bestBid >= bv.bestAsk {
		return Action{Take: true, TakeBuy: true, TakeOBX: ar.size, OrderType: "ioc"}
	}
	return Action{}
}

// --- Whale: rare large blocks, optionally iceberg-sliced ----------------------
type Whale struct {
	blockOBX float64
	iceberg  int     // number of slices (1 = single block)
	pending  float64 // remaining OBX of the current block
	buy      bool
}

func newWhale(rng *rand.Rand) *Whale {
	return &Whale{
		// Pareto(scale=1000, alpha=1.6): heavy-tailed but finite-variance whale
		// block sizes (alpha>1.5), so a single block creates a genuine shock
		// without a lone outlier dominating the whole return distribution.
		blockOBX: 1000 + pareto(rng, 1000, 1.6),
		iceberg:  1 + rng.Intn(8),
	}
}

func (w *Whale) Name() string { return "Whale" }

func (w *Whale) Decide(rng *rand.Rand, fair float64, bv bookView, inv float64) Action {
	if w.pending <= 0 {
		// start a new block in a random direction.
		w.pending = w.blockOBX
		w.buy = rng.Float64() < 0.5
	}
	slice := w.blockOBX / float64(w.iceberg)
	if slice > w.pending {
		slice = w.pending
	}
	w.pending -= slice
	return Action{Take: true, TakeBuy: w.buy, TakeOBX: slice, OrderType: "market"}
}

// forceWhale (used by scenarios) makes a whale dump/lift a given size now.
func (w *Whale) forceWhale(size float64, buy bool) Action {
	return Action{Take: true, TakeBuy: buy, TakeOBX: size, OrderType: "market"}
}
