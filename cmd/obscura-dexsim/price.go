package main

import (
	"math"
	"math/rand"
)

// ProcessKind selects the stochastic process driving the agents' REFERENCE fair
// value (the "true" price the informed traders pull the book toward). The live
// book price is emergent — it is whatever the order flow makes it — but a fair
// value anchors informed flow so the market has something to discover.
type ProcessKind string

const (
	ProcGBM  ProcessKind = "gbm"  // geometric Brownian motion (trending random walk)
	ProcOU   ProcessKind = "ou"   // Ornstein-Uhlenbeck mean reversion
	ProcJump ProcessKind = "jump" // jump-diffusion (GBM + rare Poisson jumps)
)

// PriceEngine evolves a fair value with GARCH(1,1) volatility clustering and
// Student-t (fat-tailed) innovations. One Step is one unit of simulated time.
//
// Model (log-return form):
//
//	r_t   = drift*dt + sqrt(h_t)*z_t        (z_t ~ standardized Student-t_nu)
//	h_t   = omega + alpha*eps_{t-1}^2 + beta*h_{t-1}   (GARCH(1,1) variance)
//	eps_t = sqrt(h_t)*z_t                    (the shock that feeds back into h)
//
// For OU the drift term is replaced by mean reversion toward `mean` at speed
// `theta`; for jump-diffusion a compound-Poisson jump is added with intensity
// `jumpLambda` and lognormal jump size (jumpMu, jumpSigma).
type PriceEngine struct {
	kind  ProcessKind
	price float64 // current fair value (XNO per OBX)
	logp  float64 // ln(price), evolved directly

	drift float64 // GBM/jump per-step drift of log price
	dt    float64 // time step size

	// GARCH(1,1) state
	omega, alpha, beta float64
	h                  float64 // current conditional variance h_t
	eps                float64 // last innovation eps_{t-1}
	volMult            float64 // TRANSIENT per-step variance multiplier (regime); does not compound into h

	// Student-t innovations
	nu float64 // degrees of freedom (>2 for finite variance); large -> Gaussian

	// OU params
	theta   float64 // mean-reversion speed
	logMean float64 // ln(reversion level)

	// jump-diffusion params
	jumpLambda float64 // expected jumps per step
	jumpMu     float64 // mean of log jump size
	jumpSigma  float64 // std of log jump size

	rng *rand.Rand
}

// PriceConfig holds the tunable knobs of the price/vol engine.
type PriceConfig struct {
	Kind       ProcessKind
	Start      float64
	Drift      float64
	DT         float64
	Omega      float64
	Alpha      float64
	Beta       float64
	Nu         float64
	Theta      float64
	Mean       float64
	JumpLambda float64
	JumpMu     float64
	JumpSigma  float64
}

// DefaultPriceConfig returns a sane, stylized-fact-friendly configuration: a
// GARCH process with persistent (alpha+beta≈0.97) but stationary volatility and
// fat-tailed (nu=4) innovations.
func DefaultPriceConfig(start float64) PriceConfig {
	return PriceConfig{
		Kind:       ProcGBM,
		Start:      start,
		Drift:      0.0,
		DT:         1.0,
		Omega:      2e-7,
		Alpha:      0.09,
		Beta:       0.88,
		Nu:         4.0,
		Theta:      0.02,
		Mean:       start,
		JumpLambda: 0.01,
		JumpMu:     0.0,
		JumpSigma:  0.05,
	}
}

func NewPriceEngine(cfg PriceConfig, rng *rand.Rand) *PriceEngine {
	if cfg.Beta+cfg.Alpha >= 1 {
		// keep variance stationary
		cfg.Beta = 0.99 - cfg.Alpha
	}
	if cfg.Nu <= 2 {
		cfg.Nu = 2.1
	}
	if cfg.DT <= 0 {
		cfg.DT = 1
	}
	mean := cfg.Mean
	if mean <= 0 {
		mean = cfg.Start
	}
	// unconditional variance = omega/(1-alpha-beta); seed h there.
	h0 := cfg.Omega / (1 - cfg.Alpha - cfg.Beta)
	return &PriceEngine{
		kind:       cfg.Kind,
		price:      cfg.Start,
		logp:       math.Log(cfg.Start),
		drift:      cfg.Drift,
		dt:         cfg.DT,
		omega:      cfg.Omega,
		alpha:      cfg.Alpha,
		beta:       cfg.Beta,
		h:          h0,
		volMult:    1.0,
		nu:         cfg.Nu,
		theta:      cfg.Theta,
		logMean:    math.Log(mean),
		jumpLambda: cfg.JumpLambda,
		jumpMu:     cfg.JumpMu,
		jumpSigma:  cfg.JumpSigma,
		rng:        rng,
	}
}

// Price returns the current fair value.
func (p *PriceEngine) Price() float64 { return p.price }

// Vol returns the current conditional volatility sqrt(h_t).
func (p *PriceEngine) Vol() float64 { return math.Sqrt(p.h) }

// SetVolMult sets the TRANSIENT per-step variance multiplier (regime vol). It is
// applied to sigma inside Step but never compounds into the GARCH state h, so a
// persistent regime does not make variance explode over many steps.
func (p *PriceEngine) SetVolMult(k float64) {
	if k > 0 {
		p.volMult = k
	}
}

// VolScale applies a ONE-SHOT bump to the current conditional variance h (e.g. a
// liquidity-crisis vol spike). Bounded so a single event cannot blow up the
// process. Unlike SetVolMult this does feed forward through the GARCH recursion.
func (p *PriceEngine) VolScale(k float64) {
	if k > 0 {
		p.h *= k
		// cap at 1e6x the unconditional variance to stay numerically sane.
		cap := 1e6 * p.omega / (1 - p.alpha - p.beta)
		if p.h > cap {
			p.h = cap
		}
	}
}

// Shock applies an immediate multiplicative jump of factor f to the price (e.g.
// a -0.2 news shock => f=0.8). Used by the scenario system for flash crashes /
// news. It also bumps the innovation so vol clustering follows the shock.
func (p *PriceEngine) Shock(f float64) {
	if f <= 0 {
		return
	}
	p.logp += math.Log(f)
	p.price = math.Exp(p.logp)
	p.eps = math.Log(f)
}

// SetDrift updates the per-step drift (regime switching: bull/bear/range).
func (p *PriceEngine) SetDrift(d float64) { p.drift = d }

// Step advances the engine one time unit and returns the realized log-return.
func (p *PriceEngine) Step() float64 {
	// GARCH(1,1) update of conditional variance from the LAST innovation.
	p.h = p.omega + p.alpha*p.eps*p.eps + p.beta*p.h
	if p.h < 1e-12 {
		p.h = 1e-12
	}
	sigma := math.Sqrt(p.h * p.volMult) // transient regime vol scaling

	z := studentT(p.rng, p.nu) // standardized fat-tailed innovation
	eps := sigma * z
	// feed back only the GARCH-native shock (without the transient regime mult),
	// so regime vol does not compound into the persistent variance state.
	p.eps = math.Sqrt(p.h) * z

	var r float64
	switch p.kind {
	case ProcOU:
		// OU on log price: dlog = theta*(logMean-logp)*dt + eps
		r = p.theta*(p.logMean-p.logp)*p.dt + eps
	case ProcJump:
		r = p.drift*p.dt + eps + p.poissonJump()
	default: // GBM
		r = p.drift*p.dt + eps
	}

	p.logp += r
	p.price = math.Exp(p.logp)
	if p.price < 1e-9 {
		p.price = 1e-9
		p.logp = math.Log(p.price)
	}
	return r
}

// poissonJump returns a compound-Poisson log jump for this step (≈0 most steps).
func (p *PriceEngine) poissonJump() float64 {
	// Bernoulli approximation of a rare Poisson event over one small step.
	if p.rng.Float64() >= p.jumpLambda {
		return 0
	}
	return p.jumpMu + p.jumpSigma*p.rng.NormFloat64()
}

// studentT draws a STANDARDIZED Student-t variate (unit variance for nu>2) so
// the GARCH variance interpretation stays exact. A raw t_nu has variance
// nu/(nu-2); we rescale by sqrt((nu-2)/nu). Built from a normal / sqrt(chi2/nu).
func studentT(rng *rand.Rand, nu float64) float64 {
	if nu > 200 { // effectively Gaussian
		return rng.NormFloat64()
	}
	z := rng.NormFloat64()
	chi2 := gammaVariate(rng, nu/2.0, 2.0) // chi-square(nu) = Gamma(nu/2, 2)
	t := z / math.Sqrt(chi2/nu)
	return t * math.Sqrt((nu-2)/nu)
}

// gammaVariate draws from Gamma(shape, scale) via Marsaglia-Tsang (shape>=1) with
// the standard boost for shape<1. Used to build chi-square for Student-t.
func gammaVariate(rng *rand.Rand, shape, scale float64) float64 {
	if shape < 1 {
		u := rng.Float64()
		return gammaVariate(rng, shape+1, scale) * math.Pow(u, 1.0/shape)
	}
	d := shape - 1.0/3.0
	c := 1.0 / math.Sqrt(9*d)
	for {
		x := rng.NormFloat64()
		v := 1 + c*x
		if v <= 0 {
			continue
		}
		v = v * v * v
		u := rng.Float64()
		if u < 1-0.0331*x*x*x*x {
			return d * v * scale
		}
		if math.Log(u) < 0.5*x*x+d*(1-v+math.Log(v)) {
			return d * v * scale
		}
	}
}

// ---- distributions used by the order-flow / sizing model --------------------

// expRand returns an Exponential(rate) variate: the inter-arrival time of a
// Poisson process with the given rate. Used by the event queue.
func expRand(rng *rand.Rand, rate float64) float64 {
	if rate <= 0 {
		return math.Inf(1)
	}
	return rng.ExpFloat64() / rate
}

// logNormal returns a LogNormal(mu, sigma) variate (heavy-right-tailed order
// sizes). mu/sigma are of the underlying normal.
func logNormal(rng *rand.Rand, mu, sigma float64) float64 {
	return math.Exp(mu + sigma*rng.NormFloat64())
}

// pareto returns a Pareto(xm, alpha) variate (power-law capital / whale sizes).
// Smaller alpha => heavier tail. xm is the scale (minimum).
func pareto(rng *rand.Rand, xm, alpha float64) float64 {
	u := rng.Float64()
	if u <= 0 {
		u = 1e-12
	}
	return xm / math.Pow(u, 1.0/alpha)
}
