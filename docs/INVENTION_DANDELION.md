# Invention Log — Block 4: Transaction-Origin Privacy (Dandelion++)

Produced with the invention methodology + Methodology-Tree brainstorming engine.

## 1. Challenge
Crypto-layer sender anonymity (Blocks 2-3) is undermined at the network layer:
the first node to flood a transaction reveals the originator's IP to a network
observer, deanonymizing the sender regardless of the ZK proof.

## 2. Baseline best (researched)
Plain flooding/diffusion gives weak source privacy; timing analysis locates the
origin. Bitcoin uses random diffusion delays (weak). Monero uses Dandelion++.

## 3. Brainstormed "better-than" (engine) + ranking
Engine ranked **Dandelion++** #1 with concrete Go steps: per-epoch stem peers
from outbound connections; 1-3 stem hops then fluff; `time.AfterFunc` fail-safe;
exponential per-hop delay. Risks it flagged: stem-graph learning over epochs
(mitigate by rotating peers per epoch + bounding hops), small anonymity sets for
NAT nodes, hop-count timing leak, decoy traffic bandwidth cost. Lower-ranked:
random diffusion delay (#2, weak), outbound-only relay (#3), decoy/cover traffic
(#5, 10× bandwidth). Rejected: anything requiring Tor as a hard dependency
(kept optional/future).

## 4. Decision & implementation (`pkg/p2p/dandelion.go`)
Dandelion++ two-phase propagation:
- **Stem phase:** a locally-originated tx (and received `msgStemTx`) is relayed
  to exactly ONE per-epoch **stem successor** (a random outbound peer, rotated
  every `stemEpoch`), with a small exponential delay — not flooded. The origin
  hides behind the stem path.
- **Fluff phase:** with probability `stemFluffProb` per hop (~3 hops expected),
  or if there is no stem successor, the node switches to normal flood broadcast
  (`msgTx`).
- **Fail-safe embargo:** `armEmbargo` schedules a fluff after `stemEmbargo` if
  the tx is not seen propagating, preventing black-holing. Receiving a fluff
  (`msgTx`) calls `markFluffed`, cancelling the embargo and stopping re-stemming;
  duplicate stem txs are dropped via the mempool (loop guard).

Risk mitigations applied: per-epoch stem-peer rotation (limits graph learning),
fluff probability bounds hop count, fail-safe ensures liveness. Tor/i2p remains
an optional future hardening.

Tested: `tests/critical/dandelion/` — a tx originated through the stem on one
node still reaches every node's mempool (liveness preserved under private
routing).
