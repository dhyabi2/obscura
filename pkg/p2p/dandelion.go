package p2p

import (
	"math/rand"
	"time"

	"obscura/pkg/tx"
)

// Dandelion++ transaction-origin privacy (Block 4 — see
// docs/INVENTION_DANDELION.md). A transaction first travels a private "stem":
// each node relays it to exactly ONE per-epoch stem successor (an outbound
// peer), for a few hops, before "fluffing" (normal flood broadcast). By the
// time it floods, the true origin is hidden behind the stem path — so a network
// observer cannot tie the transaction to the originating node/IP. A fail-safe
// timer fluffs the tx if it is not seen propagating, preventing black-holing.

const (
	stemEpoch     = 60 * time.Second // how often the stem successor rotates
	stemFluffProb = 0.30             // per-epoch probability the node is in fluff mode (~3 hops)
	stemEmbargo   = 8 * time.Second  // relay fail-safe: fluff if not seen propagating
	// audit fix (first-spy): the ORIGIN's fail-safe embargo is bumped LONGER than a relay's
	// so that, on a fully-stemmed or black-holed path, a DOWNSTREAM relay's timer fires
	// first and IT fluffs — not the originating node. Relays start their timers slightly
	// later (after the stem reaches them) but with the shorter base, so they still beat the
	// origin. Combined with the per-epoch fluff mode (a fluff-mode relay normally broadcasts
	// first, cancelling every embargo), this keeps the origin from being the first fluffer.
	originEmbargoBump = 6 * time.Second
	embargoJitter     = 2 * time.Second // blur absolute fluff timing across nodes
)

// stemEpochLoop rotates the stem successor each epoch (chosen from outbound
// peers, per Dandelion++).
func (n *Node) stemEpochLoop() {
	n.rotateStemPeer()
	t := time.NewTicker(stemEpoch)
	defer t.Stop()
	for {
		select {
		case <-n.done:
			return
		case <-t.C:
			n.rotateStemPeer()
		}
	}
}

func (n *Node) rotateStemPeer() {
	var outbound []*peer
	n.mu.Lock()
	for _, p := range n.peers {
		if p.outbound {
			outbound = append(outbound, p)
		}
	}
	n.mu.Unlock()
	n.stemMu.Lock()
	if len(outbound) == 0 {
		n.stemPeer = nil
	} else {
		n.stemPeer = outbound[rand.Intn(len(outbound))]
	}
	// audit fix (Dandelion++): decide stem-vs-fluff ONCE per epoch per node, not with a
	// per-tx/per-hop coin flip. A per-tx flip leaks statistically — across many txs an
	// observer separates a node's stemmed vs fluffed traffic and narrows the origin. The
	// paper's design is a per-epoch pseudorandom mode: this epoch the node either relays
	// all stem txs onward or fluffs them, so individual txs reveal nothing about origin.
	n.fluffMode = rand.Float64() < stemFluffProb
	n.stemMu.Unlock()
}

// stemRelay either continues the stem (relay to the single stem successor) or
// transitions to fluff (flood broadcast). `from` is the peer we received the
// stem from (nil if we originated the tx).
func (n *Node) stemRelay(t *tx.Transaction, payload []byte, from *peer) {
	id := t.Hash()
	n.stemMu.Lock()
	sp := n.stemPeer
	fluffMode := n.fluffMode // per-epoch per-node decision (set in rotateStemPeer)
	already := n.fluffedSeenLocked(id) // check BOTH generations, not just the live one
	n.stemMu.Unlock()
	if already {
		return
	}

	// Fluff if Dandelion is off, we have no stem successor, the successor is the
	// peer we got it from (avoid a trivial 2-cycle), or this epoch's mode is fluff.
	if !n.dandelion || sp == nil || (from != nil && sp == from) || fluffMode {
		n.fluff(id, payload, from)
		return
	}

	// Continue the stem: relay privately to the single successor, with a small
	// exponential delay, and arm the fail-safe fluff timer.
	delay := time.Duration(rand.ExpFloat64() * float64(time.Second))
	if delay > stemEmbargo/2 {
		delay = stemEmbargo / 2
	}
	go func() {
		select {
		case <-n.done:
			return
		case <-time.After(delay):
		}
		if err := n.send(sp, msgStemTx, payload); err != nil {
			// audit fix (first-spy): on a successor send failure do NOT immediately fluff.
			// An immediate fluff makes THIS node (which may be the origin) the first and
			// instant fluffer — the exact timing signal a first-spy adversary uses to
			// deanonymize. The embargo timer armed below is the fail-safe: it fluffs after
			// stemEmbargo if the tx is not seen propagating, so the tx still spreads (with a
			// uniform delay) without singling this node out as the source.
			_ = err
		}
	}()
	n.armEmbargo(id, payload, from == nil) // origin (from==nil) waits longer (anti first-spy)
}

// maxFluffedSet bounds the remembered-txid set so it cannot grow without limit
// over the node's lifetime (txids only matter for the propagation window). When
// the live set fills, it rotates to a single previous generation, so memory is
// bounded to ~2× this while recent membership is preserved.
const maxFluffedSet = 100_000

// fluffedSeenLocked reports whether id was recently fluffed (caller holds stemMu).
func (n *Node) fluffedSeenLocked(id [32]byte) bool {
	return n.fluffed[id] || n.fluffedOld[id]
}

// rememberFluffedLocked records id, rotating generations at the cap (caller holds stemMu).
func (n *Node) rememberFluffedLocked(id [32]byte) {
	if len(n.fluffed) >= maxFluffedSet {
		n.fluffedOld = n.fluffed
		n.fluffed = make(map[[32]byte]bool, maxFluffedSet)
	}
	n.fluffed[id] = true
}

// fluff floods the transaction to all peers (except the source) and records it.
func (n *Node) fluff(id [32]byte, payload []byte, except *peer) {
	n.stemMu.Lock()
	if n.fluffedSeenLocked(id) {
		n.stemMu.Unlock()
		return
	}
	n.rememberFluffedLocked(id)
	n.stemMu.Unlock()
	if except != nil {
		n.broadcast(msgTx, payload, except.conn)
	} else {
		n.broadcast(msgTx, payload, nil)
	}
}

// armEmbargo schedules a fail-safe fluff if the tx is not seen propagating.
func (n *Node) armEmbargo(id [32]byte, payload []byte, isOrigin bool) {
	d := stemEmbargo
	if isOrigin {
		d += originEmbargoBump // anti first-spy: let a downstream relay fluff first
	}
	d += time.Duration(rand.Int63n(int64(embargoJitter)))
	go func() {
		select {
		case <-n.done:
			return
		case <-time.After(d):
		}
		n.stemMu.Lock()
		done := n.fluffedSeenLocked(id)
		n.stemMu.Unlock()
		if !done {
			n.fluff(id, payload, nil)
		}
	}()
}

// markFluffed records that a tx has entered the fluff phase (seen as a normal
// broadcast), cancelling any pending embargo and stopping re-stemming.
func (n *Node) markFluffed(id [32]byte) {
	n.stemMu.Lock()
	n.rememberFluffedLocked(id)
	n.stemMu.Unlock()
}
