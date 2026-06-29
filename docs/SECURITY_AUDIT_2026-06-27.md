# Obscura Security Audit Register, 2026-06-27

Multi-agent deep audit campaign: **25 audit topics** across 6 workflows, each finding **adversarially verified (refuted-by-default)** with a mitigation search, so only genuine issues are listed.

**Confirmed findings: 85** (exploitable: **23**, hardening/dark-launched/local-disk: **62**).

## Remediation status (this pass)

**Landed (one consensus-safe batch, build + critical tests green):**
- FIXED: onion-only binds listener to loopback; no clearnet advertise fallback (pkg/p2p/p2p.go Start)
- FIXED: same Tor fail-closed change (pkg/p2p/p2p.go Start)
- FIXED: maxInboundPerGroup=4 in admitInbound (pkg/p2p/p2p.go)
- FIXED: maxInbound=24 + discoveryLoop targets outbound count (pkg/p2p/p2p.go)
- FIXED: unified mempool namespace zknull: for ZKInput+CZKSpend (pkg/mempool/mempool.go)
- FIXED: reject stray PQ fields on non-PQ tx (pkg/chain/validate.go)
- FIXED: gated behind !IsMainnet() (pkg/config/params.go)

**Deferred (need a scheduled change or design decision):**
- 🔴 **Recipient-secret nullifier (task #96)** — wires `nfspend`/`cnfspend` to fix the CRITICAL sender-can-steal + HIGH sender↔spend-linkability + MEDIUM serial-grief. Larger consensus change; the affected ZK transfer feature is dark-launched (no shipping CLI/RPC creates it), so not exploitable today, but MUST land before exposing confidential transfer.
- 🟠 **Swap reorg margin vs deep partition-recovery reorg** — `SwapReorgMargin=100` < `PoWSeedLag=512`. Raising it (and `SwapTimelockWindow`) makes swaps much slower (~17h windows); it is a parameter/UX trade-off, not a mechanical fix. Decide explicitly.
- 🟠 **Fresh-node bootstrap past PoRWindow** — needs a P2P state/snapshot-sync feature (new code), not a patch.
- 🟠 **Dandelion++ first-spy / per-hop mode** — needs a careful stem/fluff redesign; risky to patch blindly.
- Remaining mediums/lows tracked in the tables below.

---

## 1. Confirmed AND exploitable (ranked)

### [CRITICAL] Live ZK nullifier has NO ownership binding — sender can spend (steal) the coin it paid out
- **Area:** nullifier · **Category:** forgery/double-spend/theft (ownership-binding)
- **Location:** `pkg/wallet/zkspend.go + pkg/stark/spend256_air.go + pkg/stark/cspend_full.go` wallet/zkspend.go:25-29,96-98,284-289; spend256_air.go:304-320; cspend_full.go:270-290
- **Exploit:** A pays B a ZK coin (CreateCZKSpend / CreateZKMintTo). A retains serialOut,aOut,blindOut. After the coin is in the tree, A (or A racing B) constructs its OWN spend: looks up the public Merkle path for the leaf, runs ProveCSpendFull/ProveSpend256 with the known secrets, paying the value back to itself, and reveals serialOut as the nullifier — burning B's coin before B can. This is outright theft of every ZK payment, not merely a privacy leak. Reachable today…
- **Fix:** Wire the already-implemented recipient-secret circuits: replace VerifySpend256/VerifyCSpendFull in pkg/chain/zkspend.go with VerifyNfSpend/VerifyCnfSpend so the nullifier nf=H(nk,rho) and spend authority pk=H(nk,0) depend on a recipient-only secret nk that the sender never learns. The note must be paid to an ADDRESS pk_out (NfNoteFromPk) the sender knows but cannot invert to nk…

### [HIGH] Sender↔spend linkability: sender learns the nullifier of every ZK coin it sends
- **Area:** nullifier · **Category:** linkability/privacy-leak
- **Location:** `pkg/wallet/zkspend.go` 16-29, 96-98, 284-289
- **Exploit:** A pays B, records serialOut. A watches zkNull / block CZKSpend.Serial / ZKInput.Serial fields. When serialOut appears, A learns precisely when and in which tx B spent the coin A sent — defeating the unlinkability the project targets. A merchant/exchange paying out can deanonymize all downstream spends of its customers.
- **Fix:** Same fix as NULL-1: derive the nullifier from a recipient-only secret nk inside the circuit (nfspend/cnfspend). nf=H(nk,rho) where rho is delivered but nk is the recipient's key; the sender knows pk and rho but not nk, so cannot compute nf. The circuits already implement this (NfNullifier/NfNote in nfspend_air.go:33-54).

### [HIGH] Fresh node cannot bootstrap once chain exceeds PoRWindow: no state/snapshot sync, and all peers have pruned the early bodies a from-genesis sync requires
- **Area:** consensus · **Category:** availability/bootstrap (can-validate-but-cannot-mine, and cannot even sync)
- **Location:** `pkg/p2p/p2p.go, pkg/chain/forkchoice.go, pkg/chain/snapshot.go` p2p.go:589-649; forkchoice.go:186-201; snapshot.go:143-162
- **Exploit:** Deploy any new node against the live network after the chain passes height 10000. It handshakes, learns peer tip H, requests blocks H-... down toward genesis; all peers answer NOT FOUND for every height < H-10000. The new node is permanently stuck below the gap with an unconnectable chain. This is not an attack but a guaranteed liveness failure of network growth: the validator set cannot expand, contradicting the design note that 'pruned nodes can still va…
- **Fix:** Add an out-of-band bootstrap path: (a) a P2P state-sync message that ships a verified snapshot (the snapshot is already verified against header-committed roots in verifySnapshotLocked) plus the retained body window [tip-PoRWindow, tip], letting a fresh node start at tip-PoRWindow instead of genesis; and (b) allow addBlockLocked to accept a snapshot-rooted starting point (a trus…

### [HIGH] Black-hole embargo makes the ORIGIN the first node to fluff (first-spy deanonymization)
- **Area:** networking · **Category:** privacy
- **Location:** `pkg/p2p/dandelion.go` 21,80-96,135-149
- **Exploit:** Adversary becomes the victim's stem successor (boosted via many outbound-eligible connections / eclipse pressure) and black-holes the stem tx. After exactly 8s the victim floods the tx to all peers, several of which are adversary sentinels. Because the victim is the first node in the whole network to broadcast it (downstream embargos are later and never reached), the first-spy estimator attributes the tx to the victim with high confidence; repeating across…
- **Fix:** Randomize the embargo per tx (base + jitter) AND make upstream embargos LONGER than downstream so the origin is not systematically first. Better: on embargo fire the origin should re-STEM to a different successor rather than flood, only flooding after repeated failures. Never have the origin arm the shortest timer.

### [HIGH] No per-/16 cap on peer CONNECTIONS — one /16 can fill every connection slot (inbound eclipse)
- **Area:** networking · **Category:** availability/eclipse
- **Location:** `/Users/mac/XMR_alternative/pkg/p2p/p2p.go` 367-387 (admitInbound), 65 (maxInboundPerIP / maxPeers)
- **Exploit:** Attacker rents one /16 (or a cloud subnet). Opens 3 inbound connections each from ~11 IPs in that /16, all completing the magic/version handshake honestly. len(n.peers) reaches maxPeers=32, all attacker-controlled. Every honest inbound is then refused at p2p.go:377 (len>=maxPeers) and every honest dial is blocked because the pool is full. The victim is eclipsed using a single /16 at trivial cost (a handful of IPs, no mining, no misbehavior), then fed a wit…
- **Fix:** Add a per-/16 cap on CONNECTIONS in admitInbound (count peers whose ipGroup matches and reject beyond e.g. maxInboundPerGroup=8), mirroring the existing book-level maxPerGroup. This is the connection-layer analog of the address-layer defense already present.
- **Status:** ✅ FIXED: maxInboundPerGroup=4 in admitInbound (pkg/p2p/p2p.go)

### [HIGH] No reserved outbound slots — inbound flood starves all outbound connections
- **Area:** networking · **Category:** availability/eclipse
- **Location:** `/Users/mac/XMR_alternative/pkg/p2p/p2p.go` 65 (maxPeers=32), 816 (need := maxPeers/2 - len(n.peers)), 377/439 (len>=maxPeers), 847
- **Exploit:** Combine with ECL-1: attacker fills 32 inbound slots from one /16. discoveryLoop computes need = 16 - 32 = -16, so it never dials. The victim's only peers are the attacker's. Even a node that started with honest peers can be displaced after a restart — all connections drop and whoever connects-in first wins; an attacker spamming inbound connects faster than the victim's 20s discovery tick dials out.
- **Fix:** Reserve a fixed number of OUTBOUND slots inbound cannot consume (count outbound vs inbound separately; guarantee >= ~8 outbound, cap inbound at maxPeers - reservedOutbound). Keep dialing toward the outbound target regardless of inbound fill. This is the single most important structural eclipse fix.
- **Status:** ✅ FIXED: maxInbound=24 + discoveryLoop targets outbound count (pkg/p2p/p2p.go)

### [HIGH] Tor mode does not disable the clearnet inbound listener (real IP reachable/deanonymizable)
- **Area:** networking · **Category:** privacy
- **Location:** `pkg/p2p/p2p.go` 298-313, 352-364
- **Exploit:** An adversary trying to deanonymize an OBX hidden-service operator scans candidate hosts (or a target IP range) for TCP/18080, completes the OBX handshake (magic+version), and gets a valid msgHello back. This positively confirms that this clearnet IP is running an OBX node — directly linking the operator's real IP to OBX participation despite Tor being 'enabled'. Worse, an inbound clearnet peer can then feed blocks/txs and observe timing, fully defeating th…
- **Fix:** Make Tor mode fail-closed on the listener: when onionOnly is true, force the listen address to loopback (e.g. 127.0.0.1:<port>) and refuse to bind a non-loopback address, OR hard-fail at startup if --tor-proxy is set with a non-loopback --p2p bind, with a clear log line ('Tor mode requires --p2p 127.0.0.1:PORT; the Tor HiddenServicePort forwards to it'). Pass onionOnly into Sta…
- **Status:** ✅ FIXED: onion-only binds listener to loopback; no clearnet advertise fallback (pkg/p2p/p2p.go Start)

### [HIGH] Taker's XNO has no unilateral recovery — a non-cosigning maker freezes it permanently (asymmetric griefing)
- **Area:** swap · **Category:** atomicity/availability
- **Location:** `pkg/swapsession/session.go, pkg/swapd/nano.go` session.go:560-587 (VerifyFundedAndLock locks XNO), 297-345 (CoSignClaim is voluntary); nano.go:33-52 (Lock = irreversib…
- **Exploit:** Malicious maker M: (1) accepts taker T's Init, funds the OBX SwapOut correctly (passes all taker checks), sends Funded; (2) T verifies the on-chain SwapOut and locks the agreed XNO to (sA+sB)·G, sends XNOLocked + ClaimRequest; (3) M confirms the lock but NEVER sends ClaimPreSig (drops the connection / runs patched code). T cannot complete the OBX claim (lacks M's half ŝ_b), so T never reveals sA and never gets the OBX. At UnlockHeight M refunds its OBX (Re…
- **Fix:** Implement docs/SWAP_GOLIVE_PLAN.md Gap 11: give the XNO sender (taker) a unilateral recovery path. Since Nano has no native timelock, the standard scriptless construction is for the maker to hand the taker a SECOND adaptor pre-signature (over a sweep-back of the XNO to a taker address) bound to a secret the taker learns from the OBX refund, or mirror the construction so the irr…

### [HIGH] Dead-zone margin (SwapReorgMargin=100) is smaller than the chain's deepest accepted reorg (PoWSeedLag=512), so one accepted partition-recovery reorg can validate BOTH a claim and a refund for the same swap
- **Area:** swap · **Category:** atomicity/reorg-safety
- **Location:** `pkg/config/params.go` 435-457 (SwapReorgMargin def/invariant); enforcement at pkg/chain/validate.go:494-509 and pkg/swap/swap.go:183,198; reor…
- **Exploit:** Maker funds a SwapOut with absolute UnlockHeight U (= fundHeight+200). Taker confirms, locks XNO, claims the OBX on the canonical chain at height h with h+100<=U (claim mined, adaptor secret t now public, maker SHOULD sweep the XNO). A network partition lasting 101-512 blocks heals: fork Y is heavier by the PartitionRecoveryMargin and is adopted (forkchoice.go:233-237) even though it reorgs e.g. 300 deep. On fork Y the maker's funding tx is re-confirmed at…
- **Fix:** Make SwapReorgMargin track the chain's ACTUAL deepest accepted reorg, not MaxReorgDepth. Set the default to config.PoWSeedLag (512) — i.e. SwapReorgMargin must be >= PoWSeedLag — and correspondingly raise SwapTimelockWindow so SwapTimelockWindow >= SwapReorgMargin + SwapMinClaimWindow still holds (e.g. margin=512, window>=512+50). Add a startup/consensus assertion that SwapReor…

### [MEDIUM] ZK serial nullifier lives in a ~2^64 space and is minter/sender-chosen — collision/grief grinding feasible
- **Area:** nullifier · **Category:** collision/griefing
- **Location:** `pkg/stark/spend256_air.go + pkg/chain/zkspend.go` spend256_air.go:178-188,304-308; zkspend.go:192-198,262-268
- **Exploit:** Targeted grief: sender knows B's serialOut (NULL-1) and simply spends a throwaway coin minted with the same serialOut first, permanently freezing B's coin (zkNull[serialOut]=true) without B ever being able to spend. Even without NULL-1, two honest coins minted with a colliding 64-bit serial make the second unspendable once the first is spent.
- **Fix:** Make the nullifier a wide (256-bit) value bound to a recipient secret (nf=H(nk,rho), a Node256) rather than a sender-chosen 64-bit serial, and/or enforce uniqueness of serial at MINT time. The nfspend design already produces a 256-bit nf; wiring it (NULL-1) closes this too.

### [MEDIUM] Partition-recovery reorg cannot replay: kept snapshots (2 × 200) shallower than the PoWSeedLag=512 reorg depth, and the genesis-replay fallback needs PoR-pruned prefix bodies
- **Area:** consensus · **Category:** availability/consensus (partition self-healing)
- **Location:** `pkg/chain/snapshot.go, pkg/chain/forkchoice.go` snapshot.go:96-101,143-162; forkchoice.go:217-250,465-536,543-562
- **Exploit:** Network partitions for ~450 blocks (between 2×SnapshotInterval and PoWSeedLag) on a chain whose tip > 10000. When connectivity returns, the heavier (majority-hashpower) side presents a branch forking 450 blocks back with sufficient PartitionRecoveryMargin. addBlockLocked accepts it as adoptable (depth 450 <= PoWSeedLag 512, margin met), calls reorgToLocked → rebuildToBranchLocked. No snapshot <= forkHeight (tip-450) exists, resetState()+genesis replay need…
- **Fix:** Tie snapshot retention to the deepest adoptable reorg, not MaxReorgDepth: ensure at least one kept snapshot is always <= tip-PoWSeedLag. Concretely, set snapshotsToKeep >= ceil(PoWSeedLag/SnapshotInterval)+1 (with current values ceil(512/200)+1 = 4) and update the snapshot.go:96-100 comment/invariant to reference PoWSeedLag. Additionally, make the genesis-replay fallback honest…

### [MEDIUM] Reorg-failure recovery always replays from genesis (forkHeight=0), needing PoR-pruned prefix bodies — recovery is unreliable on any pruned tall chain
- **Area:** consensus · **Category:** availability/atomicity (reorg rollback safety)
- **Location:** `pkg/chain/forkchoice.go` forkchoice.go:543-562,488-499,375-391
- **Exploit:** Feed a node a partition-recovery branch whose final block is invalid (e.g. bad coinbase) but whose earlier blocks are accepted as a heavier side branch; when the heavy-enough block triggers reorgToLocked, the forward rebuild fails on the bad block, and recovery (forkHeight=0, genesis replay) fails on pruned prefix bodies. The node is left with reset/partial state and an error rather than cleanly restored on its previous valid tip.
- **Fix:** Recover to the previous branch using a snapshot at/below the actual fork point of the FAILED attempt (or below the previous tip), not forkHeight=0. Pass the real shared-prefix height so restoreSnapshotAtMostLocked finds a kept snapshot and replay starts from there using only retained bodies. Combined with PRUNE-1's deeper snapshot retention, recovery then never touches pruned p…

### [MEDIUM] Per-tx/per-hop fluff coin flip instead of per-epoch per-node stem/fluff mode (Dandelion++ deviation re-introducing v1 intersection attack)
- **Area:** networking · **Category:** privacy
- **Location:** `pkg/p2p/dandelion.go` 73
- **Exploit:** An adversary runs nodes well-connected to the mesh and becomes a victim's epoch stem successor. Over an epoch the victim forwards multiple stem txs; because each is an independent Bernoulli(0.30) trial rather than a fixed per-epoch routing decision, the adversary observes the fluff-hop distribution and applies the Dandelion-v1 intersection estimator (which ++ defeats) to attribute a fraction of those txs to the directly-upstream victim with better-than-ran…
- **Fix:** Implement the Dandelion++ epoch mode: at each stemEpochLoop rotation set a boolean stemMode = (pseudorandom < 1-q) held for the WHOLE epoch; branch on it in stemRelay instead of a per-tx rand. Keep the successor fixed per epoch (DAND-04). Derive mode/successor pseudo-deterministically from a per-epoch seed so an attacker cannot bias it by injecting txs.

### [MEDIUM] Stem-successor send failure immediately fluffs from the relaying node, revealing it as origin-candidate
- **Area:** networking · **Category:** privacy
- **Location:** `pkg/p2p/dandelion.go` 90-93
- **Exploit:** Adversary induces or waits for the victim's successor link to drop (an attacker-controlled successor resets the TCP connection right after receiving the stem tx, or the link is congested). The victim's send() errors and it floods immediately; the attacker, a peer of the victim, records it as the first fluffer and infers origin proximity. Forcing successor resets across epochs repeatedly localizes the origin.
- **Fix:** On send failure do NOT fluff; pick another outbound peer from a small ordered successor list (Dandelion++), and fall back to fluff only after exhausting the stem set, with a small random delay so the failing node is not deterministically first.

### [MEDIUM] Two-group Sybil that the victim dials can forge a false external advertise address (low quorum, no reachability check, no vote aging)
- **Area:** networking · **Category:** availability/eclipse-adjacent (self-addr poisoning)
- **Location:** `pkg/p2p/discovery.go` 60,67-102 (threshold 94); reporter attribution 81; fed from p2p.go:579-580
- **Exploit:** Attacker controls one host in 198.51.0.0/16 and one in 203.0.0.0/16. It gets both into the victim's address book (PEX or by being a seed), so the victim's group-diverse Sample() eventually dials both (both are distinct groups, neither group is saturated). On each outbound handshake the attacker echoes observedSelf = 192.0.2.250:<victimport> (an IP the attacker picks, e.g. an unrelated host or a black-holed address). After the second distinct-group report, …
- **Fix:** Harden self-discovery: (1) raise the quorum and make it relative — require agreement from a majority of CURRENTLY-CONNECTED outbound peers across >=3 distinct groups, not a fixed 2; (2) age/reset extVotes (e.g. drop votes when the reporting peer disconnects, and cap vote lifetime) so a transient 2-group Sybil cannot permanently pin a bad addr; (3) before adopting, attempt a sel…

### [MEDIUM] --tor-proxy with empty --onion-address advertises the real listen IP
- **Area:** networking · **Category:** privacy
- **Location:** `pkg/p2p/p2p.go` 305-309
- **Exploit:** Operator runs `--tor-proxy 127.0.0.1:9050 --p2p 203.0.113.5:18080` but forgets/omits --onion-address (no validation forces it). The node advertises 203.0.113.5:18080 in every msgHello and via PEX, broadcasting the operator's real IP across the whole network while believing Tor anonymized them.
- **Fix:** In onion-only mode, refuse to advertise any non-.onion address: if onionOnly && advertiseAddr is empty or not isOnion(), suppress advertising entirely (advertise "") and log a warning, or hard-fail at startup requiring a non-empty --onion-address whenever --tor-proxy is set. Never fall back to n.addr when onionOnly is true.
- **Status:** ✅ FIXED: same Tor fail-closed change (pkg/p2p/p2p.go Start)

### [MEDIUM] Maker does not arm OBX refund when taker stalls AFTER co-signing
- **Area:** swap · **Category:** availability
- **Location:** `pkg/swapnet/coordinator.go` 701-704 (driveMaker step 5), 736-762 (makerSweep returns error on timeout), 788-794 (makerRefund)
- **Exploit:** Taker drives the swap to ClaimRequest, receives ClaimPreSig, then stalls (never mines the OBX claim). makerSweep polls FindSwapSpend until c.timeout, finds no claim, returns an error; driveMaker returns leaving the OBX locked with no armed refund. With a short c.timeout vs. real OBX block times, an honest-but-slow taker can also trip this. Not fund-loss, but a liveness regression and operational footgun — maker capital is stuck until manual action.
- **Fix:** On makerSweep failure where no claim was mined, call c.makerRefund(s, mk) before returning (the refund branch is safe: Maker.Refund refuses if Phase is Claimed/Swept, so a late on-chain claim cannot be double-handled). Treat 'claim not mined by deadline' as an abort that arms the timelock refund, matching every other stall path.

### [MEDIUM] Persisted SwapState is never resumed at startup — crash-resume claim is not wired
- **Area:** swap · **Category:** availability
- **Location:** `cmd/obscura-node/swapwire.go, pkg/swapnet/coordinator.go` swapwire.go:304-335 (StateDir configured, comment claims ResumeMaker/LoadState machinery); coordinator.go:162-184 (New s…
- **Exploit:** Maker funds OBX, persists state, node crashes/restarts before completion. After restart the swap is invisible to the coordinator; no refund is armed for the funded OBX and no sweep is retried for a co-signed swap. The OBX stays locked until manual recovery (refundable on-chain via B, but no automation). Combined with SWAP-2, realistic operational failures strand maker capital.
- **Fix:** On coordinator start, scan StateDir, LoadState each file, and for maker states past PhaseFunded that are not terminal, ResumeMaker and drive recovery: if a claim is on-chain, SweepXNOIndependent; else arm the timelock refund (Maker.Refund mines forward past UnlockHeight). Add an integration test that funds, drops the coordinator, rebuilds it over the same StateDir, and asserts …

### [LOW] Mempool conflict keys for ZKInput vs CZKSpend use different namespaces while consensus unifies them — cross-path in-pool double-spend admits, invalidating templates
- **Area:** zkspend · **Category:** double-spend
- **Location:** `pkg/mempool/mempool.go` 88-93 (split keys); chain/zkspend.go:192 & 262 (unified consensus key)
- **Exploit:** A griefer broadcasts a ZKInput and a CZKSpend with the same revealed serial: both enter the pool, the miner's template includes both, validateBlockLocked rejects the block, and the miner loses the round. Repeatable cheaply. Not consensus inflation/double-spend (consensus is unified), only liveness/template-DoS.
- **Fix:** Use a single shared mempool conflict namespace for both paths (e.g. reserve 'zknull:'+Serial for CZKSpend too), matching the unified consensus 'zk:' key, so any two txs revealing the same serial conflict in the pool regardless of path.
- **Status:** ✅ FIXED: unified mempool namespace zknull: for ZKInput+CZKSpend (pkg/mempool/mempool.go)

### [LOW] PQBlindDiff not validated on the classical path → txid malleability
- **Area:** hybridval · **Category:** serialization/malleability
- **Location:** `pkg/chain/validate.go` 343-734 (absence of a PQBlindDiff check); cf. pkg/tx/tx.go:381,680,858
- **Exploit:** Take any valid classical tx, append arbitrary bytes to PQBlindDiff. CoreHash is unchanged so all proofs/sigs still verify and it passes validateTxLocked, but Hash()/txid differs. Yields distinct admissible txids for the same spend: mempool/relay confusion, broken txid-based tracking, and grinding many txids for one spend (bounded by MaxFieldBytes). Not an inflation/double-spend bug — conservation and the key-image/UTXO spent-set are unaffected.
- **Fix:** On the classical path require len(t.PQBlindDiff)==0 (mirror pqvalidate.go:114), OR include PQBlindDiff in CoreHash. Simplest: reject non-empty PQBlindDiff for any tx that is not on the PQ path.
- **Status:** ✅ FIXED: reject stray PQ fields on non-PQ tx (pkg/chain/validate.go)

### [LOW] Partial-data miner evades PoR with non-negligible probability (k=4 challenges)
- **Area:** consensus · **Category:** availability / proof-of-storage soundness
- **Location:** `pkg/config/params.go, pkg/block/por.go` params.go:66 (PoRChallenges=4), por.go:157-172 (challenge derivation)
- **Exploit:** An attacker runs a 'pruned-while-mining' node holding 50% of the 10000-block window (5000 bodies). For each new parent block it sees, it computes the 4 challenge heights (block.PoRChallengeHeight for slots 0..3); if all 4 are in its held set (~6.25% of parents), it mines on that parent and can produce a valid PoR set, contributing hashrate while storing half the data other honest full miners must store — a storage free-ride that weakens the 'miners must be…
- **Fix:** Raise PoRChallenges so f^k is negligible for any meaningful storage saving (e.g. k=16 makes f=0.9 -> 18.5%, f=0.5 -> 0.0015%), and/or make the challenge depend on the candidate's OWN nonce/coinbase (not just the parent) so it cannot be pre-filtered before doing PoW — i.e. fold the in-progress header into the seed so each PoW attempt re-randomizes which bodies are needed (the do…

### [LOW] OBX_SWAP_REORG_MARGIN consensus override is NOT behind the mainnet lock
- **Area:** consensus · **Category:** consensus
- **Location:** `pkg/config/params.go` 320-324 (gap); enforced at pkg/chain/validate.go:507 and 635, pkg/swap/swap.go:183
- **Exploit:** A mainnet operator (or a tampered/misconfigured binary) sets OBX_SWAP_REORG_MARGIN=10. Their node now ACCEPTS swap claim/refund transactions and swap-output fundings that every default-margin (100) node REJECTS: e.g. a swap claim at height where UnlockHeight-100 < height <= UnlockHeight-10 is valid on the deviating node but invalid network-wide -> a hard consensus fork for any block containing such a swap tx. Worse, it shrinks the claim/refund dead-zone th…
- **Fix:** Move the OBX_SWAP_REORG_MARGIN read inside the IsMainnet()-gated branch (or add an explicit 'if IsMainnet() { skip }' guard at params.go:320), mirroring the lock applied to the LWMA knobs at lines 190-208. SwapReorgMargin must be frozen to 100 on mainnet just like TargetBlockTime is frozen to 120. Optionally audit ALL env-driven consensus vars (SwapTimelockWindow, SwapMinClaimW…
- **Status:** ✅ FIXED: gated behind !IsMainnet() (pkg/config/params.go)

### [LOW] Stem tx admitted to every relay's mempool, leaking pre-fluff (still-in-stem) transactions
- **Area:** networking · **Category:** privacy
- **Location:** `pkg/p2p/p2p.go` 660-668
- **Exploit:** Adversary polls reachable nodes' mempools right after seeing nothing on the flood network; the handful holding a not-yet-fluffed tx are the stem path. Intersecting the earliest holders localizes the origin neighborhood, partially defeating the stem.
- **Fix:** Hold stem-phase txs in a separate, non-publicly-queryable stem buffer and promote to the queryable mempool only on fluff. Use an explicit stem-seen set for the loop-guard rather than relying on mp.Add's duplicate detection so eviction cannot cause re-stem loops.

---

## 2. Confirmed, not currently exploitable (hardening / dark-launched / local-disk)

| Sev | Area | Finding | Location | Why not exploitable now |
|---|---|---|---|---|
| medium | consensus | short | `x` | pkg/chain/snapshot.go:101 (snapshotsToKeep=2), :35 (SnapshotInterval=200); pkg/chain/forkchoice.go:4… |
| low | zkspend | Confidential spend uses the coin's own commitment-serial as the nullifier (not a derived… | `pkg/stark/cspend_full.go` | pkg/wallet/zkspend.go:25-28 (high-entropy per-output serial derivation) + pkg/chain/zkspend.go:262-2… |
| low | crosspath | zkNull/pqNull not header-committed nor verified in snapshot restore | `pkg/chain/snapshot.go, pkg/block/block.go` | none (verifySnapshotLocked omits zkNull/pqNull); reachability mitigated by absence of peer snapshot-… |
| low | stealth | PQ wallet scanning requires a full ML-KEM-768 decapsulation per on-chain output (no chea… | `pkg/pqstealth/stealth.go` | pkg/chain/pqvalidate.go:123-129 (MaxOutputs cap + MinFeePerByte anti-spam bound the linear cost) |
| low | lattice | Binding is computationally broken at the illustrative parameters (weak SIS, classical BK… | `pkg/pqcommit/ajtai.go` | pkg/pqcommit/ajtai.go:34-38 (params explicitly labeled ILLUSTRATIVE/research-only, off default conse… |
| low | lattice | Aggregate BlindDiff bound is the correct logical fix but is instantiated at the same wea… | `pkg/pqtx/ledger.go` | pkg/pqtx/ledger.go:157-173 (the bound: maxAbs=(1+len(Outputs))*RandB rejects oversized BlindDiff); p… |
| low | lattice | BlindDiff is excluded from the signed CoreHash (malleability of the conservation witness… | `pkg/pqtx/pqtx.go` | pkg/chain/pqvalidate.go:114 (if len(t.PQBlindDiff) != 0 { return ...errValidation }) forbids PQBlind… |
| low | wotsplus | Consensus never prevents WOTS+ root (R) reuse across outputs — WOTS one-time invariant i… | `pkg/chain/pqvalidate.go` | pkg/pqsign/hybrid.go:52-66 (GenerateHybrid always draws a fresh random wSeed via rand.Read, so hones… |
| low | wotsplus | Prototype pqtx ledger binds nullifier to WOTS root R only (the bug the live path documen… | `pkg/pqtx/ledger.go` | pkg/pqtx/pqtx.go:1 and pkg/pqtx/ledger.go:1 both carry //go:build pq; `go list -deps ./cmd/...` conf… |
| low | wotsplus | chain/parseHybridSig does not length-validate the inner Schnorr/Wots blobs | `pkg/chain/pqvalidate.go` | pkg/pqsign/hybrid.go:136 (HybridVerify rejects len(sig.Schnorr)!=64 and len(P)!=32, len(R)!=PubKeySi… |
| low | mlkem | Detection tag / amount keystream / MAC are bound only to the KEM shared secret, not to t… | `pkg/pqstealth/stealth.go` | pkg/pqtx/account.go:110-111 and pkg/pqwallet/pqwallet.go:110-112 (a.owned[OneTimeKey] ownership cros… |
| low | pqaccum | Snapshot pqUtxo amounts are not bound by any committed root (integrity gap) | `pkg/chain/snapshot.go` | pkg/chain/snapshot.go:256-277 verifySnapshotLocked; consumers loadSnapshotLocked/restoreSnapshotAtMo… |
| low | pqaccum | RestoreState silently accepts truncated/corrupt snapshots via short Read | `pkg/pqaccum/snapshot.go` | pkg/chain/snapshot.go:268-271 verifySnapshotLocked compares pqAcc.Root() to tip.PQAccRoot; pkg/chain… |
| low | consensus | PoR randomness is grindable by the parent's miner (PoW-cost bias, not unbiasable) | `pkg/block/por.go` | No mitigation. pkg/block/por.go:129-139 porSeed derives the challenge seed solely from prevHash; pkg… |
| low | networking | Restart does not resist a prior eclipse — no anchor reconnect to last-good peers | `/Users/mac/XMR_alternative/pkg/p2p/p2p.go` | addrbook.go:62-65 (re-apply per-/16 maxPerGroup cap on load), addrbook.go:88-92 (Add enforces maxPer… |
| low | networking | Sanitization permits unbounded loopback/private addresses on public networks; book dilut… | `/Users/mac/XMR_alternative/pkg/p2p/discovery.go` | pkg/p2p/addrbook.go:88-90 (maxPerGroup=32 per /16 cap in Add), :81-83 (maxAddrBook=4096 total cap), … |
| low | networking | Group ban score only rises on protocol misbehavior, not on eclipse-consistent clean beha… | `/Users/mac/XMR_alternative/pkg/p2p/p2p.go` | pkg/p2p/p2p.go:366-387 admitInbound enforces maxPeers=32 and maxInboundPerIP=3 (per single IP) plus … |
| low | networking | Adopted self-address is never reachability-validated, so any wrong observation silently … | `pkg/p2p/discovery.go` | pkg/p2p/discovery.go:24-33 (isRoutable) and :60,94 (minSelfDiscoveryGroups=2), plus pkg/p2p/p2p.go:5… |
| low | networking | IPv6 reporter/peer grouping is /32, weaker than the /16 IPv4 intent for cheap hosting ra… | `pkg/p2p/addrbook.go` | pkg/p2p/addrbook.go:30-43 (ipGroup); discovery.go:81,94 (self-discovery uses ipGroup and requires mi… |
| low | networking | Onion-only address filter is bypassed for seeds and for the PEX/msgGetAddr response (cle… | `pkg/p2p/p2p.go` | pkg/p2p/transport.go:31-44 (torDialer/NewTorDialer SOCKS5) + pkg/p2p/p2p.go:402,852 (all dials go th… |
| low | economics | Cumulative emitted-supply uint64 counter overflows ~10.4 years into tail emission (deter… | `pkg/chain/apply.go` | pkg/chain/apply.go:256-260 overflow guard clamps c.emitted to ^uint64(0) on wrap; pkg/config/params.… |
| low | economics | Vault state and incentivePool are not committed to any block-header root (snapshot integ… | `pkg/block/block.go:27-40 ; pkg/chain/snapshot.go:47,232,…` | pkg/chain/snapshot.go:258-277 (verifySnapshotLocked anchors acc/null/pq/cm roots); snapshots are loc… |
| low | economics | Coinbase Fee field is unvalidated (ignored) — minor txid-malleability, no value impact | `pkg/chain/validate.go` | No mitigation exists for the Fee field specifically. The coinbase-only-field guard at pkg/chain/vali… |
| low | swap | Taker lacks a durable at-most-one nonce guard (relies on single-call discipline); rb-sty… | `pkg/swapsession/session.go` | pkg/swapnet/coordinator.go:479 (driveTaker calls BuildClaimRequest exactly once); pkg/swapsession/se… |
| low | swap | same-host SwapID rebind relaxes the sender-binding (F-C) authenticator to host granulari… | `pkg/swapnet/coordinator.go` | pkg/swapnet/coordinator.go:333-339 (same-host gate, not arbitrary rebind); pkg/swapnet/coordinator.g… |
| low | swap | OBX_SWAP_REORG_MARGIN env override is bounded only by SwapTimelockWindow, not by the dee… | `pkg/config/params.go` | pkg/config/params.go:321 bounds the override only as `n > 0 && n < SwapTimelockWindow` (200); there … |
| low | xnoleg | Cross-rate enforced ONLY by AcceptInit offer gate; HandleInit amount check is tautologic… | `pkg/swapsession/session.go; pkg/swapnet/coordinator.go; …` | coordinator.go:537 `if c.acceptInit == nil || !c.acceptInit(init, peer)` deny-by-default; swapwire.g… |
| low | xnoleg | LockInfo does not assert the block subtype is 'send' | `pkg/swapd/nanorpc.go` | pkg/swapsession/session.go:286-291 — maker requires accountPub==XNOAccountPub (joint (sA+sB)·G key, … |
| low | xnoleg | Maker does not re-confirm the lock is cemented/unspent immediately before sweeping | `pkg/swapd/nanorpc.go; pkg/swapsession/session.go` | Maker sweeps only after ConfirmXNOLock cemented the lock and after co-signing; finishSweep re-verifi… |
| low | xnoleg | Taker funding secret accepted as a CLI flag (process-list / argv exposure) | `cmd/obscura-node/main.go` | cmd/obscura-node/main.go:74 default is os.Getenv(OBX_NANO_FUND_SECRET) (env path doesn't touch argv)… |
| info | zkspend | Fused confidential circuit (cspendFullCircuit) is consensus-live but has NO production c… | `pkg/chain/zkspend.go` | none |
| info | zkspend | Fused AIR fully constrains all four sub-relations (membership, nullifier, range, balance… | `pkg/stark/cspend_full.go` | pkg/chain/validate.go:257 (fee range), pkg/chain/validate.go:262-268 (shared nullifier set), pkg/sta… |
| info | zkspend | Trace masking (zk_mask.go) is the standard coset-LDE + Z_H·r construction; no witness le… | `pkg/stark/zk_mask.go` | pkg/stark/air.go:140 (maskColumn applied to every witness column); pkg/stark/zk_mask.go:74; pkg/star… |
| info | crosspath | incentivePool/emitted/swapNonces restored from snapshot without root check | `pkg/chain/snapshot.go` | pkg/chain/snapshot.go:258-277 (verifySnapshotLocked verifies accumulator/null/pq/cm roots but NOT em… |
| info | crosspath | pqRoots anchor set unbounded; transparent-anon and ZK/PQ isolation verified correct | `pkg/commit/oneofmany.go, pkg/chain/pqvalidate.go, pkg/ch…` | pkg/chain/validate.go:418-433 (transparent), pkg/chain/validate.go:446-471 (anon), pkg/chain/apply.g… |
| info | nullifier | ZK nullifier set (zkNull) is not committed in the header nullifier accumulator | `pkg/chain/apply.go + pkg/chain/validate.go` | pkg/light/light.go:7-11 (SPV client does not use NullRoot / does no double-spend checking); pkg/chai… |
| info | nullifier | Class-group EqualExp nullifier binding is experimental and unwired; PoKE r-bound is perm… | `pkg/accumulator/nullifier.go` | pkg/accumulator/nullifier.go:21-25 (self-documented soundness scope); not imported on consensus path… |
| info | stealth | PQ KEM ciphertext and detection tag are not consensus-bound to the output's one-time key… | `pkg/chain/pqvalidate.go` | pkg/pqwallet/pqwallet.go:110-113 (Scan requires owned[OneTimeKey]); pkg/chain/pqvalidate.go:76-78 (E… |
| info | stealth | Classical stealth output fields not validated as well-formed at consensus (recipient-sid… | `pkg/wallet/wallet.go` | pkg/chain/validate.go:300-339 (partial: OneTimeKey IS canonical/non-identity validated at :305; Comm… |
| info | stealth | Address checksum is a 4-byte BLAKE2b truncation (typo guard only); view-key export corre… | `pkg/commit/address.go` | none |
| info | lattice | pqcommit is a building block, NOT on the live consensus path — consensus PQ value model … | `pkg/chain/pqvalidate.go` | pkg/chain/pqvalidate.go:76-81 (rejects non-empty Commitment/EncAmount/MAC; validates public Amount <… |
| info | wotsplus | WOTS+ chain/root hashing lacks an explicit per-function domain tag (relies on input-leng… | `pkg/pqsign/wots.go` | pkg/pqsign/wots.go: all sizes are compile-time constants (n=32, w=16, wlen=67), so role input length… |
| info | wotsplus | pqstealth MAC is 128-bit (64-bit post-quantum) — adjacent param-hardening note | `pkg/pqstealth/stealth.go` | pkg/chain/pqvalidate.go:76-78 (reserved confidential-amount fields Commitment/EncAmount/MAC must be … |
| info | mlkem | kdf uses unlength-prefixed variadic concatenation (canonical-encoding ambiguity, defense… | `pkg/pqstealth/stealth.go` | pkg/pqstealth/stealth.go:48-57 (kdf), 112-121 (seal), 143-166 (Scan); pkg/chain/pqvalidate.go:76-78 … |
| info | mlkem | FO implicit rejection, constant-time comparisons, and amount confidentiality model are c… | `pkg/pqstealth/stealth.go` | pkg/pqstealth/stealth.go:48-57 (versioned, label-domain-separated kdf), :117,:164 (subtle.XORBytes),… |
| info | mlkem | Trial-decapsulation scanning is O(outputs) ML-KEM decaps and not rate-limited, but is no… | `pkg/pqwallet/pqwallet.go` | pkg/chain/pqvalidate.go:10,71 (consensus validator only reads pqstealth.TagSize for a length check; … |
| info | pqaccum | RestoreState peak/leaf counts (np, n) are unbounded attacker-influenced allocation | `pkg/pqaccum/snapshot.go` | Snapshots are local-only: pkg/chain/snapshot.go restoreSnapshotAtMostLocked reads from bolt; no netw… |
| info | pqaccum | pqRoots anchor set grows unbounded (acknowledged liveness/memory TODO) | `pkg/chain/apply.go` | apply.go:130 Len()>0 guard prevents empty-set anchor; pqvalidate.go:169 anchor whitelist check + lin… |
| info | hybridval | pqRoots PQ anchor set is unbounded (no rolling window) | `pkg/chain/apply.go` | Reorg restore at snapshot.go:75 (capture PQRoots) / snapshot.go:237 (restore) and forkchoice.go:448 … |
| info | hybridval | parseHybridSig length arithmetic is unsafe only on 32-bit int platforms (info) | `pkg/chain/pqvalidate.go` | build.sh PLATFORMS = linux/amd64,linux/arm64,darwin/amd64,darwin/arm64,windows/amd64,windows/arm64 (… |
| info | consensus | Header.Version is unconstrained by consensus (committed but not validated against an all… | `pkg/chain/validate.go` | None. A miner can set Header.Version to any value; this only changes the block id/preimage (so it mu… |
| info | consensus | Verification asymmetry and PoW/replay binding are sound (defense confirmation) | `pkg/chain/por.go, pkg/block/por.go, pkg/block/block.go` | pkg/chain/por.go:52 (NumTxs==len(Txs) enforced); por.go:65 (PoRRootOf==Header.PoRRoot); por.go:70-77… |
| info | consensus | Fork-choice cumulative work trusts the block's CLAIMED difficulty, not the LWMA-expected… | `pkg/chain/forkchoice.go` | pkg/chain/validate.go:70-72 (h.Difficulty != expDiff rejection on activation); pkg/chain/forkchoice.… |
| info | networking | Self-discovery only on outbound conns + own-port clamp correctly prevent the documented … | `pkg/p2p/discovery.go` | pkg/p2p/discovery.go:60 (minSelfDiscoveryGroups=2), :73-77 (own-port clamp via n.addr), :81 (reporte… |
| info | networking | Seedless PEX convergence and poisoned-peer-set resistance are adequately bounded (positi… | `pkg/p2p/addrbook.go` | pkg/p2p/addrbook.go:88-89 (maxPerGroup=32 cap in Add), :60-66 (cap re-applied on load), :81 (maxAddr… |
| info | networking | observedPeer (source-address echo) is sent even in Tor mode — defense-in-depth | `pkg/p2p/p2p.go` | pkg/p2p/p2p.go:579 (observedSelf only acted on if p.outbound); pkg/p2p/discovery.go:70 (loopback obs… |
| info | economics | incentivePool overflow guard silently drops the pool contribution instead of clamping | `pkg/chain/apply.go` | pkg/chain/validate.go:178 (totalVaultYield > c.incentivePool rejects); pkg/chain/apply.go:223-227 (p… |
| info | economics | PQ transaction fee is burned, not credited — conservative (deflationary), documented | `pkg/chain/validate.go` | pkg/chain/pqvalidate.go:206-213 (exact overflow-checked equality inSum == outSum + t.Fee, inSum summ… |
| info | economics | t.Fee (miner-collected fee) has no explicit upper bound — bounded only indirectly by con… | `pkg/chain/validate.go` | pkg/chain/validate.go:577 (publicOut := t.Fee), :727 (VerifyConservationGen forces fee funded by rea… |
| info | swap | 3-layer F-1 fund-freeze enforcement and SwapTimelockWindow composition are correct (defe… | `pkg/chain/validate.go` | pkg/chain/validate.go:635 (consensus F-1 margin check); pkg/swapsession/session.go:242 (maker Fund m… |
| info | xnoleg | raw<->offer-unit rounding asymmetry (floor) on the maker reservation path | `pkg/swapd/nano.go; cmd/obscura-node/swapwire.go` | Payment path uses raw XNOAmount directly (session.go:578 Lock; session.go:289 exact-equality co-sign… |
| info | xnoleg | Confirmed positives: authoritative-lock verification, finality handling, and unilateral … | `pkg/swapsession/session.go; pkg/swapd/nanorpc.go` | session.go:268-291 (Confirmed+exact account/amount checks); nanorpc.go:345-356 (cemented), 372-416 (… |

---

## FINAL REMEDIATION STATUS (2026-06-27, verified green)

**FIXED + verified (build + full pkg/chain + tests/critical all green; droplet-smoke + 2-node WAN tested):**
- 🔴 CRITICAL — ZK spend no ownership binding (sender can steal/link) → recipient-secret nullifier `nf=H(nk,rho)` wired end-to-end (`cnfSpendCircuit`/`VerifyNfSpend`/`VerifyCnfSpend`); address publishes `NfPk=H(nk,0)`; adversarial `TestNfRecipientSecretOwnership` proves sender-cannot-steal; `NetworkSeed` genesis reset; `MaxBlockBytes` 2→4MB for the larger proofs.
- 🟠 HIGH — swap reorg-margin vs deep partition reorg → `SwapReorgMargin = PoWSeedLag`, `SwapTimelockWindow = PoWSeedLag+100` (happy path unchanged).
- 🟠 HIGH — Dandelion++ first-spy → per-epoch stem/fluff mode + origin-waits-longer embargo + send-failure-no-fluff.
- 🟠 HIGH — eclipse: no /16 connection cap → `maxInboundPerGroup=4`; no reserved outbound → `maxInbound=24` + `discoveryLoop` targets outbound count.
- 🟠 HIGH — Tor not fail-closed → onion-only binds loopback, never advertises clearnet.
- 🟡 MED — swap maker refund-arming after co-sign stall (height-based sweep-or-refund); swap crash-resume (`Coordinator.Resume()`, `TestMakerCrashResume`); self-discovery Sybil (threshold 2→4); `OBX_SWAP_REORG_MARGIN` mainnet-lock.
- 🟢 LOW — mempool nullifier-namespace unify; `PQBlindDiff` txid-malleability reject.

**DEFERRED (need a decision or a large feature; NOT safe to auto-change):**
- 🟠 HIGH — fresh-node bootstrap past PoRWindow → needs a new P2P state/snapshot-sync subsystem.
- 🟠 HIGH — taker-XNO unilateral recovery → inherent to scriptless Nano (no timelocks); a protocol "fix" risks a worse vuln. Leg-ordering bounds the risk.
- 🟢 LOW — partial-data PoR evasion (raise `PoRChallenges`, a security/cost tuning decision); stem-tx-in-mempool leak (Dandelion++ stem/mempool separation, subtle).

**Note:** deploying the nf-genesis to the LIVE testnet droplets is a genesis reset (wipes the current chain) — left for operator go-ahead. All droplet testing was non-destructive (temp datadirs, separate ports; the live systemd testnet was untouched).
