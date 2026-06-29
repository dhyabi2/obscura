# Obscura XNO↔OBX Swap — 105 Issues (audit)

_Generated 2026-06-26 by parallel bug-hunt workflow. STATUS: findings, triage pending._

I'll produce the deduplicated, ranked report directly from the JSON. No tools needed — this is analysis and synthesis.

# Obscura XNO↔OBX Swap — Definitive Issue Report

**Summary:** 105 distinct issues after dedup (from 143 raw). **Severity tally:** 28 critical · 44 high · 30 medium · 3 low.

---

## A. Atomicity, secret-reveal & timelock safety

**[1] OBX secret sA is revealed before the XNO lock is cemented** — `critical` · cmd/obscura-swap waitForReceivable/doAtomicSwap · in-process
Impact: `waitForReceivable` returns on first sighting of any receivable and the OBX claim publishes sA against a not-yet-cemented (replaceable) Nano send; if it never cements the OBX leg is lost. `Confirmed()` exists but is never called on the live path.
Fix: After `Receivable()`, poll `nano.Confirmed(hash)` until cemented (with timeout) and re-verify the credit before settling OBX; fix the misleading "Detected + confirmed" log.

**[2] Stranded first-send: user's XNO is locked into an executor-only joint account before OBX can fund** — `critical` · runLive waitForReceivable→doAtomicSwap · in-process
Impact: `newSecrets()` mints both sA and sB locally; the user sends real XNO first, and any post-lock OBX failure (insufficient funds, mining/broadcast/Extract/crash) leaves XNO recoverable only by the executor — Nano has no timelock. Textbook first-mover loss, structural not racy.
Fix: Split the joint key maker/taker (user keeps their own share); fund and confirm the OBX leg on-chain BEFORE prompting for XNO; provide a real XNO-side abort.

**[3] Received XNO amount is never validated against the agreed amount** — `critical` · waitForReceivable/doAtomicSwap vs --xno-amount-raw · validation
Impact: `--xno-amount-raw` is used only for a display string; the OBX leg settles a fixed 3 OBX regardless of how much XNO arrives. A counterparty sending 1 raw still drains the full 3 OBX.
Fix: Pass expected amount (as big.Int) into `waitForReceivable`; keep polling/reject until `amt >= expected` AND confirmed before settling.

**[4] Live OBX leg settles a hardcoded 3 OBX, ignoring the matched offer entirely** — `critical` · runLive/doAtomicSwap (obxAmt := 3*AtomicPerCoin) · before
Impact: Live path always locks 3 OBX with no reference to any Offer's Give/Get amounts; the order book's prices/amounts have zero binding to what is locked. No agreed quote (rate/amount/expiry) binds both legs.
Fix: Thread the selected Offer (or explicit --obx/--xno amounts) into doAtomicSwap, derive OBX from the agreed rate, and refuse if expired or mismatched.

**[5] Joint address advertised as fundable before the OBX wallet can actually fund** — `critical` · runLive STATUS panel / setupOBXLeg vs FundSwap · before
Impact: The panel tells the user to send XNO once Balance()>0, but FundSwap needs 3 OBX + fee (subject to CoinbaseMaturity/subsidy) and only runs after the XNO lock; if short, XNO is already locked into an unfundable swap.
Fix: Before printing the joint address, assert `Balance() >= obxAmt+fee` (dry-run FundSwap); only advertise/accept once the OBX side is provably fundable.

**[6] Status panel falsely asserts "if the swap stalls funds are reclaimable"** — `critical` · runLive STATUS panel · refund
Impact: On a funds-handling screen the panel claims reclaimability, but the user's XNO has no refund, the OBX reclaim is unwired, and the only "recovery" is operator-held secrets — false-safety statement.
Fix: Replace with an honest experimental warning until true key-splitting + refund make the claim accurate.

**[7] Refund/timelock spend path exists in consensus but is never built, broadcast, or watched** — `critical` · wallet.BuildSwapSpend(isRefund=true) — only caller passes false · refund
Impact: VerifyRefund and the consensus refund branch exist, but no orchestration ever builds a refund tx, waits for UnlockHeight, or exposes a refund command — funded OBX is abandoned on stall. Dead code from the user's perspective.
Fix: Add an executable refund flow (CLI subcommand + doAtomicSwap error path) that waits to UnlockHeight, builds/mines/confirms the refund spend.

**[8] No refund/abort path is executed; no timeout coordination or deadline staggering** — `critical` · doAtomicSwap (unlock=height+200, no refund branch) · after
Impact: doAtomicSwap funds then unconditionally claims/sweeps; a stalled real swap hangs with no watchtower, no height watcher, no staggered deadlines deciding who acts first.
Fix: Add a deadline state machine — funder arms a refund timer at UnlockHeight; claimer claims before a safety margin; stagger OBX vs cross-chain timelocks; persist deadlines to survive restart.

**[9] Timelock ordering (L_obx < L_btc) is never computed or enforced** — `critical` · doAtomicSwap (unlock=height+200) + bitcoin.FundHTLC locktime · validation
Impact: The cardinal HTLC invariant (secret-revealing party gets the longer refund window with margin) is unrelated across legs; consensus accepts any UnlockHeight. A malicious counterparty can overlap windows and refund one leg while claiming the other.
Fix: Compute both deadlines from one base with a minimum safety gap (per-chain block time), reject setups below the gap, and surface/assert ordering before funding.

**[10] OBX timelock is a magic height+200, uncoordinated with XNO finality** — `high` · doAtomicSwap line 156 · validation
Impact: The single timelock is fixed regardless of XNO confirmation time or the up-to-30-min receivable wait; on a slow/forking chain the refund can race the claim or the claim can miss the window.
Fix: Derive the timelock from a negotiated, conservatively large duration (wall-clock→blocks via TargetBlockTime) exceeding max claim-propagation + reorg; make it a party-agreed parameter.

**[11] No reorg/grace margin at the claim/refund height boundary — both paths can spend after a reorg** — `high` · validate.go:493-512; swap.VerifyClaim/VerifyRefund · validation
Impact: claim valid `height<UnlockHeight`, refund `height>=UnlockHeight`, no buffer; a reorg across the boundary lets a claim (now orphaned) and a refund each be valid on different forks, so the funder refunds while the counterparty already extracted the secret. The PoW chain reorgs freely.
Fix: Require refund only at `UnlockHeight + reorgDepth`; require the claimant to claim a safe margin earlier; pick a margin ≥ practical max reorg depth.

**[12] Extract/sweep mismatch aborts after the OBX claim is already committed — no salvage** — `medium` · doAtomicSwap step 3 (pt(accountSecret) != xnoPub) · after
Impact: After sA is revealed and OBX moved, a recovered-key mismatch returns an error before any sweep attempt, stranding the XNO with no fallback dump/retry.
Fix: Verify Extract round-trips (against PreVerify/Adapt) BEFORE mining the claim; on mismatch, log both candidate scalars and still attempt the sweep / hand off to manual recovery.

**[13] Adaptor nonces ra,rb / R can be reused on retry, leaking the 2-of-2 key shares** — `high` · doAtomicSwap (ra,rb,R generated once) · in-process
Impact: A retry of doAtomicSwap for the same swap produces a fresh pre-signature over a different coreHash with the SAME ra,rb — classic nonce-reuse key recovery of a,b.
Fix: Derive nonces deterministically (RFC6979-style over a||coreHash) so message↔R is fixed, or forbid re-running a swap whose claim was partially built; treat (a,b,ra,rb) as single-use per swap id.

**[14] Consensus does not enforce ClaimR adaptor-nonce uniqueness across swaps** — `medium` · validate.go swap-output path · validation
Impact: Consensus checks ClaimR is a canonical point but not its uniqueness; reusing R across two swap claims under the same aggregate key leaks the secret, letting a counterparty drain a sibling swap.
Fix: Treat (ClaimKey,ClaimR) as a uniqueness nullifier; reject a SwapOut whose ClaimR collides with any live/historical swap.

**[15] Claim/refund signed message omits the swap's economic terms** — `low` · tx.CoreHash (SwapInputs→{SwapKey,IsRefund}) · validation
Impact: The signature covers only SwapKey+IsRefund; Amount/ClaimKey/UnlockHeight/ClaimR/ClaimT rely entirely on the registry lookup — fragile against any future re-registration or 32B SwapKey collision.
Fix: Fold the resolved entry's (Amount, Claim/RefundKey, UnlockHeight, ClaimR, ClaimT) into the signed message as defense-in-depth.

---

## B. Two-party protocol, counterparty trust & message-passing

**[16] No take/accept handshake — the order book is a billboard, not a swap protocol** — `critical` · p2p msgSwapOffer/msgGetOffers; rpc handleOffers/handlePostOffer · before
Impact: The entire P2P swap surface is "gossip offer" + "pull offers"; there is no take/accept/reject/session message, so two independent parties can never reach doAtomicSwap's pre-agreed starting point.
Fix: Add a swap-session sub-protocol (TakeOffer/Accept/Reject) with a persisted state machine (proposed→accepted→funded→claimed/refunded).

**[17] Per-swap crypto material is generated in one process and never exchanged** — `critical` · newSecrets/doAtomicSwap (sA,sB,a,b,ra,rb,popA,popB,R,T) · before
Impact: One struct mints both sides' secrets; there is no wire exchange of public shares, no DLEQ binding tying sA to T, so two distrusting parties cannot construct a single byte of the swap.
Fix: Define a key-exchange round (public shares + PoP + public nonces) with a DLEQ proof binding sA↔T; each side recomputes K, R, joint Nano pubkey and aborts on mismatch.

**[18] Adaptor pre-signature is computed from BOTH secret keys in one call** — `critical` · swap.CoSignClaim(a,b,ra,rb,…) · in-process
Impact: CoSignClaim takes both parties' full scalars and combines locally; there is no partial-signature exchange, no verification of a received partial, and no defense against adaptive-nonce (Wagner) attacks.
Fix: Split into PartialPreSign(myKey,myNonce,e)→s_i + CombinePreSig with per-partial verification (s_i·G == Ri + e·X_i), using commit-then-reveal nonces.

**[19] Live/selftest are self-swaps: one executor plays maker, taker, and counterparty** — `high` · runLive (us/user both FromSeed; sec holds sA+sB; generates --xno-dest) · in-process
Impact: Both wallets and the destination are local; the executor knows sA AND sB, so atomicity is never exercised against an adversary — a custodial loop dressed as a swap.
Fix: Split roles into distinct processes/keys exchanging only public data; add a two-node integration test where completion requires honest cooperation or refund.

**[20] No counterparty/session authentication or replay protection; no encrypted channel** — `medium` · p2p msgSwapOffer/msgGetOffers; Offer.Maker bare pubkey · validation
Impact: Offer.Maker is a bare key with no authenticated transport; once secret-exchange exists, a MITM could substitute public shares (B/S_b) and steal a leg, and stale-session messages can be replayed.
Fix: Establish an authenticated/encrypted session (Noise/X25519) proving control of Offer.Maker, bind each message to a fresh session ID + nonces, and sign public shares.

**[21] Funding-order / who-funds-first race is undefined — first funder griefable into locked-capital DoS** — `high` · doAtomicSwap step 1 (FundSwap with no counterparty precondition) · in-process
Impact: OBX is funded with no check that the cross-chain leg is locked+confirmed; whoever funds first ties up capital for the full timelock while the counterparty walks away free and repeats.
Fix: The longer-timelock party funds first; the second funds only after verifying the first leg is funded AND confirmed (Confirmed with a depth threshold); gate the claim on cross-chain-lock confirmation; consider an offer stake.

**[22] Counterparty has no monitor to extract the on-chain-revealed secret and sweep before its refund window** — `high` · doAtomicSwap Extract from in-process `pre`; swap.ClaimBindingOK · after
Impact: Atomicity relies on the funder scanning for the claim, running Extract on the published sig, and sweeping — but `pre` is an in-memory var from the same process and no chain-scanner exists, so a real claimee never learns the secret.
Fix: Build a per-session monitor that stores the pre-sig, watches for the swap-key spend, runs/verifies Extract, and sweeps the cross-chain leg, surfacing its state.

**[23] Offer has no on-chain/escrow binding — makers can spoof liquidity they don't hold and abandon takes** — `medium` · swapbook Offer/Verify (PoW+expiry+sig only) · before
Impact: Verify never proves the maker holds GiveAmount; with no take/accept, N takers race the same single-unit offer and the maker abandons every take at zero cost.
Fix: Require a refundable bond or proof-of-funds reference verifiable before committing capital, plus offer reservation/locking during an active session.

**[24] Offer signature is not bound to the SwapOut the maker must fund** — `low` · swapbook Offer.Sign/Verify vs chain SwapOut · before
Impact: The signature proves key ownership but carries no commitment to actually lock GiveAmount nor a link to a specific ClaimKey/SwapKey — costless griefing/withholding.
Fix: Bind offers to a pre-funded SwapOut or bonded deposit the taker can verify before committing capital.

---

## C. Persistence, idempotency, recovery & resume

**[25] Executor crash between lock and sweep loses the in-memory joint key** — `critical` · runLive (swapSecrets in-memory, logged once) · in-process
Impact: sA/sB/a/b live only in memory plus one log line; a crash/kill/lost-scrollback after the user sends XNO makes the locked XNO permanently unspendable.
Fix: Persist per-swap state (sA,sB,a,b, joint addr, lockID, swapKey, unlock height, phase) to a 0600 file BEFORE printing the joint address; add a resume path that completes the sweep or refund.

**[26] No idempotent resume — every run mints a brand-new joint account** — `high` · runLive · validation
Impact: runLive always calls newSecrets() and shows a new joint address; if the user sent XNO to a prior run's address, re-running never detects it and those funds sit forever.
Fix: Persist the active swap under --obx-datadir and on startup detect/reuse the same joint address, jumping to the receivable/sweep phase.

**[27] Recovery secrets (sA, sB, dest) are emitted only to stdout/log** — `high` · runLive log.Printf · before
Impact: The only copy of funds-recovery material is a log line; lost scrollback after a lock = permanently unrecoverable XNO. No durable machine-readable persistence.
Fix: Atomically write sA, sB, joint addr, lockID, dest secret to a 0600 recovery file before advertising the joint address; document recovery from the file, not logs.

**[28] Sweep is not idempotent — re-run after a partial sweep double-receives and aborts** — `critical` · nanorpc.Sweep · validation
Impact: On a re-run, the now-opened account makes Sweep try to receive the already-consumed lock again; Nano rejects it, the whole sweep aborts, and the received-but-unsent balance is stranded with no fast-path.
Fix: At the top of Sweep, check Receivable: skip the receive if the lock is gone; if balance>0 with no receivable, go straight to send-all on the current frontier. Make each leg independently resumable.

**[29] Crash between receive and send strands funds; no persisted resume across the boundary** — `high` · nanorpc.Sweep (two publishState calls); doAtomicSwap · refund
Impact: receive succeeds, send fails/dies; account is opened with balance but no record survives, and the single Sweep call has no resume logic — operator must hand-craft blocks from logged sA/sB.
Fix: Make Sweep resumable (if opened with balance==recvAmt, skip receive, send to dest on live frontier); persist (sec, lockID, recvHash, dest) for automated retry.

**[30] MockNano cannot model a stalled/aborted swap or refund — tests give false recovery confidence** — `high` · MockNano (Lock/Sweep/Confirmed); runSelfTest · validation
Impact: The mock has no return-to-sender, no timelock, and Confirmed() just returns "lock exists"; the selftest only exercises the happy path, which is how the stranded-send and dead-refund bugs shipped.
Fix: Add failure-injection tests (claim withheld→OBX refund; abort after Lock→XNO recoverable; crash/resume) and gate the PASSED banner on them.

**[31] Cleanup tears down OBX side but never finalizes/flushes Nano state on the error path** — `medium` · setupOBXLeg close funcs · after
Impact: A post-claim error runs deferred close() then log.Fatalf, discarding in-memory joint secrets unless the user scraped the early log.
Fix: Before any Fatalf on the post-lock path, write the recovery bundle to a known file and print its path; add an explicit finalize-before-cleanup step.

**[32] No RPC failover despite presets encoding CanWork/CanProcess** — `high` · nanorpc.call; nanopresets.PublicNanoRPCs · in-process
Impact: call() retries the SAME url 3×; if the single chosen node dies mid-sweep the receive may publish but the send fail on all retries, stranding funds opened-but-unswept.
Fix: Carry the full preset list and fail over to the next CanProcess/CanWork endpoint; persist recvHash so retry resumes at the send step.

**[33] Public-RPC work_generate is a single point of failure (only rainstorm does work)** — `medium` · nanopresets + nanorpc generateWork/publishState/Sweep · after
Impact: All work routes to rainstorm; if it rate-limits/blocks/down, Sweep cannot generate work and the locked XNO is stuck until manual recovery.
Fix: Allow multiple work+process endpoints with failover, support local work generation, and emit an explicit "XNO stuck, recover with sA+sB" path instead of reporting completion.

---

## D. Sweep / completion-detection / fund-finalization

**[34] No completion verification after Sweep — success assumed from a returned hash** — `high` · nanorpc.Sweep; doAtomicSwap step 3 · validation
Impact: Sweep returns nil once `process` echoes a hash; it never confirms cementation or that dest's balance rose, so "atomic swap complete" prints while funds may still be in the joint account.
Fix: Poll block_info(sendHash) until confirmed and/or Receivable(dest)/Balance(dest) before reporting completion; return the send hash to the caller.

**[35] Sweep publishes the send immediately after the receive without confirming the receive** — `high` · nanorpc.Sweep/publishState · after
Impact: The chained send's `previous` points at an unconfirmed/unpropagated receive; under public-RPC load this yields Gap/Old/Fork rejections, leaving the account opened with the full balance but not swept.
Fix: After the receive `process` succeeds, poll block_info(recvHash) until confirmed and frontier==recvHash before building the send; treat fork/gap/old-block as retryable by re-reading the frontier.

**[36] User left holding an UNOPENED receivable at the destination** — `high` · nanorpc.Sweep send to destPub · after
Impact: The final send only creates a receivable at dest; a fresh/unopened dest (the auto-generated case) requires a manual open block, yet the tool logs "swept back" implying finality.
Fix: Document that dest must pocket the receivable; for the generated-dest path publish the open/receive block (we hold the key), or refuse to generate a dest.

**[37] Auto-generated --xno-dest secret is logged once — lost logs = unrecoverable swept funds** — `high` · runLive (generated dest, secret printed once) · after
Impact: A successful sweep sends to a generated account whose secret exists only in one log line; losing it loses the funds even on "success."
Fix: Refuse the live swap without an explicit user-controlled --xno-dest; if generation is kept for selftest, write the secret to a permissioned file with explicit acknowledgement.

**[38] Sweep receives only one block but sends whole balance — extra receivables stranded** — `medium` · nanorpc.Sweep (recvAmt vs send balance 0) · after
Impact: Multi-send scenarios leave other pending receivables unreceived at the joint account with no recovery path, and any pre-existing balance is also swept.
Fix: Loop Receivable() to receive ALL pending before the final send-all (or at minimum log every remaining receivable hash); assert exactly the locked amount before sweeping.

**[39] Racy/non-retrying frontier read makes process reject fork/old-block** — `high` · nanorpc.Sweep account_info read; publishState · in-process
Impact: account_info's error is ignored (`_ =`), so on an RPC hiccup `prev` is treated as unopened and an invalid open block is built on a non-empty account; no rebuild on process failure.
Fix: Propagate the account_info error; on process failure re-fetch frontier/balance and rebuild; explicitly distinguish opened vs unopened from a successful read.

**[40] call() blind-retries non-idempotent process/send RPCs** — `medium` · nanorpc.call (3 attempts) used by Lock(send), publishState(process) · in-process
Impact: A node that processed a block but timed out the HTTP response gets the action re-sent; for wallet `send` this can double-send, and a cemented `process` may return an error envelope treated as failure.
Fix: Auto-retry only idempotent reads; for `process`, treat old-block/fork after timeout as success via block_info; for `send`, query for the created block before resending.

**[41] Hardcoded difficulty for work can go stale and cause "insufficient work" with no escalation** — `medium` · nanorpc.publishState/generateWork · in-process
Impact: Difficulty strings are hardcoded per subtype; if the network raises thresholds or the work endpoint doesn't honor the param, `process` rejects and the opening receive fails after the funding send already left, stranding the lock.
Fix: Fetch active_difficulty (or read it from the rejection) and regenerate work at the required level; retry at higher difficulty on a work-low error.

**[42] uint64 amount type cannot represent ≥1 XNO (1 XNO = 1e30 raw)** — `high` · nano.NanoClient Lock/Balance; nanorpc.Balance; xnoRaw · validation
Impact: uint64 caps ~1.8e19 raw (~1.8e-11 XNO); Lock truncates and Balance clamps >64-bit to max, so any realistic-sized swap is silently mishandled and completion checks built on Balance are meaningless.
Fix: Widen amount to big.Int / decimal-raw-string across NanoClient and callers (Receivable already returns a string); gate the live path to refuse amounts above the uint64 ceiling until widened.

**[43] Receivable picks the LARGEST pending block — wrong/attacker block can drive settlement** — `high` · nanorpc.Receivable; waitForReceivable · validation
Impact: The joint address is public; anyone can send a larger (or dust) amount, and the executor locks onto that block, settling the full OBX leg against it while the real lock may be left pending.
Fix: Select the receivable by expected amount (within tolerance) and ideally known source/send-block hash; receive ALL pending so nothing is stranded.

**[44] callOnce re-marshals the whole generic map to re-decode — fragile, swallows marshal error** — `low` · nanorpc.callOnce · in-process
Impact: The double JSON round-trip only works because Nano returns strings; a number-typed field, duplicate `error` key, or non-object top-level decodes inconsistently, and the inner marshal error is dropped.
Fix: Decode once into a struct embedding the typed out plus an `Error` field (or Unmarshal the same bytes into out); don't swallow the error.

**[45] Sweep doesn't reconcile that exactly the locked amount is swept** — `medium` · nanorpc.Sweep (afterRecv=curBal+recvAmt; send balance 0) · in-process
Impact: With multiple receivables only one is received and the rest abandoned; sending balance→0 also sweeps any unrelated pre-existing balance if the address was reused.
Fix: Enumerate all receivables and assert exactly one matches the lock; assert afterRecv equals the expected locked amount before sweeping to zero.

**[46] Default representative is a single hardcoded remote account** — `medium` · nanorpc.defaultNanoRep (used in Sweep open block) · in-process
Impact: A decommissioned/offline rep weakens confirmation if the account lingers/reuses, and it's exactly the third-party hardcoding the file otherwise forbids.
Fix: Make the open-block representative operator-configurable (default to the configured Source account's own rep, or self-represent since the account is swept to empty).

---

## E. Preflight, status panel & RPC trust

**[47] Lock detection trusts a single operator RPC's "receivable" with no cross-check** — `critical` · waitForReceivable; nanorpc Receivable/Confirmed · before
Impact: Every XNO fact comes from one endpoint; a hostile/lying/compromised RPC can report a non-existent or unconfirmed lock as real, making the executor reveal sA and pay OBX for XNO that never cements.
Fix: Poll Confirmed until cemented and cross-check critical reads (block_info/confirmed/amount) against ≥2 independent endpoints, erroring hard on disagreement.

**[48] Status panel tells the user to send XNO even when the Nano RPC is UNREACHABLE** — `high` · runLive (nodeStatus UNREACHABLE branch) · before
Impact: On a Version() error the panel still says "SEND your XNO now" and proceeds to poll; if the RPC is truly down the later same-endpoint Sweep can't complete and the locked XNO can't be swept.
Fix: Treat an unreachable RPC as a hard preflight failure (or require --force); don't solicit a send until version + a work_generate/process capability probe succeed.

**[49] Single trusted RPC for all reads enables silent lies in either direction** — `high` · nanorpc Receivable/Confirmed/Balance/Sweep · before
Impact: One endpoint can claim a fake lock is confirmed (pay OBX for nothing) or hide a real send (sweep silently fails); no second source, SPV, or quorum.
Fix: Require agreement from ≥2 independent RPCs on block_info/confirmed/amount before settling; verify receivable amount via an independent block_info and require cementation from a second endpoint.

**[50] No TLS/endpoint authentication — MITM can forge every Nano answer** — `high` · nanorpc.callOnce; nanopresets · before
Impact: Default http.Client (no pinned CA, no min TLS, http:// allowed) plus total RPC trust makes a network MITM equivalent to a lying RPC — fabricate a confirmed lock or suppress the sweep.
Fix: Enforce https for non-loopback hosts, set a minimum TLS version, support cert/SPKI pinning, and document that fund safety depends on channel integrity.

**[51] --xno-amount-raw is display-only; no expected-amount guard before the lock** — `medium` · runLive amtNote + waitForReceivable · validation
Impact: The flag never reaches waitForReceivable, which accepts any receivable ≥1 raw; a user told to send N raw can underpay and OBX still settles.
Fix: Derive the expected XNO amount from the quote, pass it into waitForReceivable, and accept only a matching (within tolerance) receivable before settling.

**[52] setupOBXLeg funding loop can spin forever with no timeout or diagnostics** — `medium` · setupOBXLeg (loop until Balance>0); syncToNetwork 90s give-up · before
Impact: If the executor can't win/validate blocks (epoch/PoWSeed mismatch silently `continue`d in netMineLoop), the loop never exits and never reports why; syncToNetwork proceeds with 0 peers.
Fix: Add an overall timeout + progress logging (height delta, peers, mined-vs-rejected) and surface netMineLoop AddBlock/template errors instead of silently continuing.

**[53] netMineLoop self-cancel race / concurrent AddBlock on one chain** — `medium` · netMineLoop vs mineWith · in-process
Impact: Both netMineLoop and mineWith mine+AddBlock on the same chain c; the height watcher self-cancels on our own blocks and two goroutines racing the tip cause repeated template/AddBlock errors, starving the swap tx.
Fix: Serialize block production — submit swap txs into a mempool a single miner goroutine drains, or pause netMineLoop while mineWith runs.

---

## F. Bitcoin (HTLC) leg

**[54] BTC HTLC leg has zero orchestration and is never bound to the OBX adaptor secret** — `high` · bitcoin.go FundHTLC/Redeem/Refund/BtcHTLCScript (no non-test callers) · validation
Impact: Nothing sets the BTC hashlock = SHA256(t) of the OBX-committed adaptor secret, verifies the funded HTLC matches agreed terms before OBX funds, or coordinates locktimes — the "atomic" guarantee is unimplemented for BTC, yet BTC offers can match.
Fix: Add a btcAtomicSwap orchestrator (derive hashlock from T, verify witness program/amount/locktime/pubkeys before funding OBX, confirmation-gated redeem/CLTV refund); until then disable BTC offers.

**[55] BTC redeem reveals the preimage in the mempool — front-runnable** — `high` · bitcoin.Redeem/RevealedPreimage; wallet.html mock disclaimer · after
Impact: The redeem exposes the adaptor secret in the Bitcoin mempool; a watcher/miner can grab the still-open second leg if it isn't destination-bound, and the orchestrator has no BTC path at all.
Fix: Confirmation-gated, deadline-aware redeem/refund with asymmetric timelocks (secret-revealing leg redeemed second with margin) and RBF fee-bumping; document the ordering requirement.

**[56] BTC vs XNO refund semantics differ with no unified abort handling** — `medium` · bitcoin.Refund (CLTV height) vs swap.VerifyRefund (UnlockHeight) vs nano (no timelock) · refund
Impact: XNO has no native timelock (refund "anchored on OBX"), BTC uses an independent CLTV, and no orchestrator path fires the OBX refund or recovers a stranded XNO lock; cross-leg timelock asymmetry is unenforced.
Fix: Enforce cross-leg ordering (secret-revealer gets the shorter window) and implement an abort/refund path per leg that the live flow actually invokes on timeout.

**[57] MockBitcoin.FundHTLC ignores the redeem script / witness program / pubkey lengths** — `medium` · bitcoin.MockBitcoin.FundHTLC/btcLock · validation
Impact: The mock only checks len(hash)==32 and non-empty pubkeys, never building/comparing BtcHTLCScript/BtcWitnessProgram nor enforcing 33-byte pubkeys — so a swap that passes selftest can fail on real bitcoind; the mock is an unreliable atomicity oracle.
Fix: Require 33-byte pubkeys, compute the script via BtcHTLCScript, and store/compare BtcWitnessProgram as the lock identity so Redeem/Refund validate against the real script.

---

## G. Order book: validation, units & matching

**[58] "BTC (mock)" asset label is sent verbatim and rejected by validAsset — every BTC offer is dead on arrival** — `critical` · wallet.html oGive/oGet options → obxBuildOffer → swapbook.validAsset · before
Impact: The `<option>` text "BTC (mock)" (space + parens) becomes GiveAsset; validAsset allows only [A-Za-z0-9.-], so Verify() fails and Book.Add rejects it — yet the UI shows "Posted ✓". BTC side is impossible to create, not merely mock-backed.
Fix: Use `<option value="BTC">BTC (mock)</option>`, map labels→canonical tickers before obxBuildOffer, and validate the ticker client-side with a clear error instead of grinding PoW.

**[59] Best() ranks on un-normalized float atomic ratio across assets** — `high` · swapbook.Book.Best (ratio=float64(Give)/float64(Get)) · validation
Impact: Give/Get are raw units in each asset's own scale; comparing as a bare float makes "best price" meaningless across assets, and uint64≈2^53+ values lose precision so an attacker can craft a numerically-best but economically terrible offer.
Fix: Carry per-asset decimals from an allowlisted registry and rank with normalized big.Rat / integer cross-multiplication; reject amounts exceeding supply or losing precision.

**[60] Best() silent NaN/Inf on zero amounts and non-deterministic ties** — `medium` · swapbook.Best · validation
Impact: Best operates on List() (not the Add-validated set); a zero-GetAmount offer yields +Inf and always "wins," and equal-best offers resolve in random map order, sometimes picking a worse offer due to float rounding.
Fix: Cross-multiply with big.Int, defensively skip zero-amount offers, and break ties on offer ID.

**[61] UI offerRate and swapbook.Best use opposite ratio conventions and opposite extremes** — `high` · wallet.html offerRate (get/give, min) vs swapbook.Best (give/get, max) · validation
Impact: The UI "best" chip (min get/give) and the matching engine flag different offers as best, and the UI's min get-per-give is actually the worst price for a taker — backwards.
Fix: Define one canonical taker-receives-per-pays price used in both places, maximize it, label the chip accordingly, and add a test asserting UI-best == Best() for a pair.

**[62] handleOffersJSON "rate" is an un-normalized %g atomic ratio, lossy and inverted vs Best()** — `medium` · rpc/server.go handleOffersJSON · after
Impact: Rate = get/give as a raw float with %g (scientific notation, precision loss for XNO-scale), sorted ascending and re-parsed — a misleading "price" field that disagrees with the wallet and isn't the same top-of-book as Best().
Fix: Drop Rate or compute a normalized human price with a decimals table; sort via exact integer cross-multiplication; align convention with Best().

**[63] JSON rate sort (and wallet sort) is non-deterministic on ties** — `medium` · rpc/server.go handleOffersJSON sort.Slice; wallet renderSwap · after
Impact: No tie-break + unstable sort makes equal-rate rows reorder between requests, so the 2000-row truncation drops a different arbitrary subset and the book visibly jitters.
Fix: Add a deterministic secondary key (offer ID, then maker) using SliceStable.

**[64] Offer Expiry is maker wall-clock, unbound to swap timelocks; clock-skew prune is non-deterministic** — `medium` · swapbook Offer.Expiry/Verify/pruneLocked · validation
Impact: Expiry is unrelated to on-chain timelocks and pruned by each node's local clock, so a taker can accept an offer that dies mid-swap and the book is inconsistent across peers.
Fix: Require remaining TTL to exceed the swap's worst-case completion time, and have the taker re-validate Expiry against a confirmed chain reference at lock time.

**[65] Offers carry no netID binding and no settlement address — replayable cross-network** — `medium` · swapbook Offer.Core/Verify · validation
Impact: Core() omits config.NetID(), so a signed offer is valid on any Obscura instance, and it commits to no cross-chain endpoint — enabling bait offers settled off-band on different terms.
Fix: Mix domain-separated NetID() into Core() and add a settlement-binding field (maker's Nano account / BTC refund pubkey commitment).

**[66] Cheap 12-bit PoW + no per-maker cap enables sybil offer flooding** — `medium` · swapbook OfferPoWBits=12, Book.Add · before
Impact: ~4096 hashes per offer with MaxBookSize=50000 and no per-maker quota lets one attacker fill the book, evict honest offers, and force a Schnorr VerifyFull on every node; dust offers are admissible (only GiveAmount==0 rejected).
Fix: Raise PoW to a meaningful cost, add a per-maker live-offer cap, and enforce a minimum economic amount per asset.

**[67] No reciprocity/self-trade/sybil guard — wash/fake-depth costs only OfferPoWBits=12** — `high` · swapbook OfferPoWBits, Book.Add; obxBuildOffer maker key from seed · before
Impact: The maker key is fixed per seed and there's no per-maker rate limit or self-trade detection, so one wallet floods thousands of self-dealing offers and the un-grouped Σ/best/depth UI advertises fabricated liquidity dominated by one maker.
Fix: Cap offers-per-maker, raise/asset-tier PoW, and have the UI annotate depth with distinct-maker count and dedup same-maker offers.

**[68] XNO=2x liquidity-reward rule subsidizes the cheapest leg to wash; burn/anchor defenses unshipped** — `medium` · docs/LIQUIDITY_REWARDS.md MULT_BPS{XNO:20000}; nano.go feeless · before
Impact: The feeless/timelock-free XNO leg is the cheapest to fabricate yet most-subsidized, while the burn/anchor/dedup defenses that make self-dealing net-negative exist only in spec.
Fix: Do not enable any reward until burn+anchor and the sybil-mesh regression test land; keep the 2x strictly distributional under a budget cap with asset-tiered PoW/burn on XNO and a kill-switch.

**[69] Re-gossip relays raw payload with no per-peer rate limit — relay amplification** — `low` · p2p msgSwapOffer · in-process
Impact: Cheap 12-bit valid offers are flood re-broadcast to all peers with only a flat penalty, amplifying bandwidth before MaxBookSize caps memory.
Fix: Per-peer token-bucket on msgSwapOffer accept/relay, higher/count-scaled PoW, and per-maker live-offer cap.

**[70] pruneLocked is an O(n) full-map scan under the global lock on every Add/List** — `low` · swapbook Add→pruneLocked · in-process
Impact: Near 50000 entries, each Add/List scans the whole map under one mutex, serializing gossip ingestion and the /offers handler — a cheap throughput-degradation vector.
Fix: Maintain an expiry-ordered index (min-heap / time buckets) and/or prune on a background ticker.

**[71] msgGetOffers replies with an arbitrary 256-offer map-order subset — book can't reconcile** — `medium` · p2p msgGetOffers (obook.List() map order) · after
Impact: With >256 offers each request returns a different random subset, so a late-joining peer can never converge on the full book and some offers never propagate within TTL.
Fix: Order the response (SortedByID) and paginate with a cursor, or switch to an inventory/getdata have-want pattern.

**[72] In-memory-only book is lost on restart with no persistence or rebroadcast** — `medium` · swapbook NewBook/offers map; p2p obook · after
Impact: A maker's own offers vanish on restart and are never re-gossiped, so an offer that reached a now-departed subset can disappear network-wide before Expiry.
Fix: Persist locally-originated live offers and rebroadcast on startup and on a periodic timer until Expiry; snapshot the book to disk.

---

## H. Wallet UI (web)

**[73] OBX decimals are 8 in the DEC map but OBX is 12 — every OBX price is off by 10,000×** — `critical` · wallet.html DEC={OBX:8,…} vs ATOMIC=1e12 · ui-ux
Impact: offerRate/best/Σ/depth divide OBX by 10^8 instead of 10^12, so every OBX-leg human rate, summary chip, and depth label is wrong by 10^4 — a taker reads a grossly mispriced offer.
Fix: Set DEC.OBX=12 (derive from the shared AtomicPerCoin constant) and add an assertion DEC.OBX === log10(ATOMIC).

**[74] Wallet shows offers without verifying signature, PoW, or expiry** — `high` · wallet.html loadOffers → obxParseOffer (no Verify) · before
Impact: loadOffers only parses; a malformed-but-parseable or already-expired offer (from an unpruned node) renders as a tradeable row, misleading the taker.
Fix: Expose a WASM Verify path, call o.Verify(now), and filter out failing/expired offers in loadOffers.

**[75] Per-pair "Σ" liquidity sums raw atomic give-amounts across assets/decimals** — `high` · wallet.html buckets[pair].liq += Number(give_amount) · ui-ux
Impact: Σ adds un-normalized atomic amounts (10^-12 OBX vs 10^-30 XNO) with no asset label, and Number() loses precision above 2^53 for XNO — a huge, mislabeled, wrong total implying false depth.
Fix: Normalize to human units via the (fixed) DEC map before summing, accumulate as BigInt, divide only at display, and label with the give-asset ticker.

**[76] Depth chart cumulates raw atomic give across both pair directions — not a real depth curve** — `high` · wallet.html depthBars() · ui-ux
Impact: Mixing 10^12- and 10^30-scale amounts plus both trade directions in one ascending-rate stack, with Number() precision loss, makes the cumulative bars uninterpretable and visually misleading.
Fix: Restrict depthBars to a single directed pair, normalize via BigInt before cumulating, separate bid/ask stacks, and label the axes in human terms.

**[77] depthBars sorts by a rate that mixes human and raw ratios** — `medium` · wallet.html sort by offerRate().rate · ui-ux
Impact: offerRate falls back to a raw atomic ratio whenever an asset is missing from DEC (every BTC offer today), so the same set is sorted by 10^4..10^18-scale and human ratios mixed — a non-monotonic price axis.
Fix: Never mix human/raw in one ordering; refuse to draw depth (or normalize all) once any asset's decimals are unknown, and drop the user-facing "raw" fallback.

**[78] Wallet renders raw maker amounts un-scaled by decimals** — `high` · wallet.html renderSwap (give_amount verbatim) · ui-ux
Impact: "Maker gives" prints the raw atomic integer (e.g. 100000000) with no division, so the user can't tell it's 1 OBX and the depth axis mixes 8- and 30-decimal raws.
Fix: Format give/get through the DEC table per the offer's asset and only sum/compare like-denominated human amounts.

**[79] Wallet fetches uncapped /offers (up to 50000 rows), WASM-parsing each — freezes the tab** — `medium` · wallet.html loadOffers uses /offers · ui-ux
Impact: /offers has no server cap (unlike /offers/json's 2000); the wallet runs a blake2b WASM ID-hash per offer and builds a 50000-row DOM, freezing the browser.
Fix: Call /offers/json (decoded, capped, rate-sorted) and additionally cap rendered rows (e.g. top 200) with a "showing N of M" note.

**[80] Amount inputs require raw atomic units with no helper, conversion, or human echo** — `high` · wallet.html oGiveAmt/oGetAmt placeholders · before
Impact: Users hand-type 12- or 30-zero integers; one missing/extra zero changes the price 10× and is irreversible once posted, and placeholders imply wrong units (5e7 raw XNO ≈ 0).
Fix: Accept human decimal amounts per the selected asset, convert in JS, and show a live "offering X OBX for Y XNO (rate …)" preview.

**[81] No confirmation/preview before signing and posting an offer (irreversible, hidden 1h expiry)** — `high` · wallet.html postOffer() · in-process
Impact: One click grinds PoW and broadcasts a signed, publicly-gossiped offer with an unshown 1h expiry and no review — a fat-fingered amount is published with no chance to catch it.
Fix: Insert a confirm modal restating assets/human amounts/computed rate/expiry before grinding PoW.

**[82] "Posted ✓" / "Broadcast ✓" success is shown even when peers will reject** — `high` · wallet.html postOffer line 381; send line 254 · after
Impact: Success resolves on any 2xx from the relay, but an offer that fails Verify on peers (bad asset, expired, amount=0) is dropped network-wide while the user is told it succeeded; sends only confirm bytes-accepted, not mined.
Fix: Re-query /offers and confirm the offer id is present before showing success; poll txids and reflect mempool→confirmed for sends.

**[83] No "My offers" status lifecycle after posting** — `medium` · wallet.html postOffer · after
Impact: After "Posted ✓" there's no open/matched/taken/expired status and the 1h expiry is never shown — fire-and-forget for a swap, exactly the "stuck vs working?" question users have.
Fix: Track posted offers in localStorage and render a "My offers" list with live status derived from the book and a countdown to expiry.

**[84] Post button never disabled during PoW grind — double-clicks post duplicates and freeze the tab** — `medium` · wallet.html postOffer/send/vaultDeposit/vaultClaim · in-process
Impact: Synchronous WASM PoW freezes the UI with only a small "grinding…" text; the button stays clickable so impatient users queue a second offer/broadcast.
Fix: Disable the button + set aria-busy at start, re-enable in finally; run PoW in a Web Worker.

**[85] Swap tab is view/post-only: no take/execute/refund/abort/monitor UI** — `critical` · wallet.html #t-swap · in-process
Impact: The page advertises "OBX↔XNO is the working path" and lets users post offers, but the only action is postOffer — no take button, no per-swap progress, no tx hashes, no timer; all real execution is in the out-of-band swapd CLI with the unrecoverable-funds issues above. Users can't see or recover anything.
Fix: Add a live swap-execution stepper (Matched→XNO locked→OBX funded→OBX claimed→XNO swept) driven by polling with per-step hash/height and spinner/done states, or clearly relabel the tab as posting-only/experimental.

**[86] Recovery phrase shown plaintext, stored unencrypted in localStorage, auto-restored, weak warning** — `high` · wallet.html createWallet/.seedbox/auto-restore/footer · before
Impact: No re-entry verification, cleartext localStorage('obx_mnemonic'), silent auto-restore with no lock, and the only warning is buried in the footer — funds lost via XSS, shared computer, or browser sync.
Fix: Require phrase re-entry/word-confirm, encrypt with a user passphrase (or offer "don't remember on this device"), and place the plaintext warning inside the create flow.

**[87] msg()/table rendering uses innerHTML with un-escaped backend text (XSS); vague amount errors; silent rounding** — `medium` · wallet.html msg(); send "bad amount/fee"; obxToAtomic · validation
Impact: Backend error strings and parsed offer output are injected via innerHTML (script injection); send() shows only "bad amount/fee" for any failure; obxToAtomic silently truncates to 6 decimals so 0.0000001 OBX rounds to zero with no warning.
Fix: Use textContent/escape in msg() and table rendering, give field-specific validation messages, and warn when an amount is rounded or below the minimum unit.

**[88] Address copy gives no confirmation and silently no-ops without the clipboard API** — `medium` · wallet.html copyAddr() · after
Impact: `navigator.clipboard?.writeText` with optional chaining and no feedback does nothing on http/older browsers/denied permission; the user thinks they copied the receive address and pastes stale/empty content. No keyboard/screen-reader access either.
Fix: Add a "Copied ✓" toast, a textarea/execCommand fallback, and make the address a real role=button with aria-label and keyboard handling.

**[89] No in-wallet way to take/accept an offer — swap tab is read-only** — `medium` · wallet.html SWAP section · before
Impact: Order-book rows have no Take/Accept control; a user expecting to "swap" posts an offer and waits indefinitely with no in-wallet completion path and no match indication.
Fix: Add a Take flow (even if it just emits the swapd command/HTLC params) or relabel the tab as advanced/read-only with an explanation.

**[90] Asset rate/depth math uses JS Number for XNO=30-decimal amounts — precision loss** — `medium` · wallet.html offerRate/Σ/depthBars · before
Impact: Number(o.give_amount) on XNO amounts up to 1e30 rounds above 2^53, corrupting displayed rates, Σ chips, and depth widths even though the underlying offer hex is fine.
Fix: Aggregate with BigInt and compute rates via a fixed-point helper (scale down by decimals as BigInt before Number).

**[91] Pair filtering keys on give→get with no canonical pair or case-normalization** — `low` · wallet.html buckets/renderSwap key · ui-ux
Impact: "OBX→XNO" and "XNO→OBX" show as two unrelated markets (forcing mental rate inversion) and "obx"/"OBX" bucket separately, fragmenting the same market.
Fix: Use a canonical sorted-pair key rendering bid/ask sides together with inverted rates, and normalize/uppercase tickers.

**[92] Offer table never shows expiry or maker** — `medium` · wallet.html table header/renderSwap · ui-ux
Impact: A 6-hour offer looks identical to a 5-second one; a taker can pick one that expires before the multi-step swap completes, wasting HTLC setup. The id cell hardcodes "open."
Fix: Add an Expiry countdown column (colored when low) and a truncated maker column.

**[93] Order-book amounts/rates shown as raw with a meaningless "raw" ratio** — `medium` · wallet.html renderSwap/fmtRate · before
Impact: Enormous unlabeled atomic integers plus a mix of human and " raw" rates make it impossible to judge fairness; the depth chart inherits the same raw amounts.
Fix: Format to human units per asset decimals with the ticker; never display a raw atomic ratio — label unsupported assets instead.

---

## I. Explorer & observability

**[94] Atomic-swap order book is invisible in the explorer despite full backend + proxy support** — `high` · explorer.html (no offers UI) vs /offers/json + api/explorer.js · observability
Impact: /offers/json (decoded, rate-sorted) is exposed and proxy-whitelisted, yet explorer.html never renders it — the project's flagship swap liquidity is unobservable.
Fix: Add an order-book panel polling get('offersjson') with rows (give→get, amount, rate, expiry countdown) and empty/error states.

**[95] Network-hashrate card divides NEXT-block difficulty by PAST block intervals** — `high` · explorer.html refreshSummary · observability
Impact: s.difficulty is the requirement for the unmined next block, divided by intervals of blocks mined at different difficulties, so the estimate is biased whenever difficulty adjusts (the common case) and can't be computed correctly without per-block difficulty.
Fix: Add `difficulty` to ExplorerBlockSummary and compute Σ(per-block difficulty)/elapsed over the window; at minimum label it "est."

**[96] explorerTx undercounts outputs and misclassifies ZK/vault txs** — `high` · rpc/explorer.go explorerTx · observability
Impact: NumOutputs omits VaultOutputs and ZKOutputs and the kind switch has no zk/vault case, so vault-deposit and the flagship ZK-anon-spend txs report wrong counts, are mislabeled "confidential," and drop their one-time keys from public view.
Fix: Include VaultOutputs/ZKOutputs in NumOutputs, add zk/vault kind cases, append their one-time keys, and update explorer.html COLORS/CSS.

**[97] pow_backend always empty in the explorer summary** — `medium` · rpc/explorer.go handleExplorerSummary · observability
Impact: ExplorerSummary.PoWBackend is never assigned, so the footer "PoW: …" label never renders and users can't see the mining backend.
Fix: Set resp.PoWBackend = pow.BackendName (mirror /status) and add a non-empty test.

**[98] Hashrate single-block fallback fabricates a value from difficulty/target** — `medium` · explorer.html refreshSummary else branch · observability
Impact: With <2 blocks, hr = difficulty/target restates the target ratio as a confident "MH/s," indistinguishable from a real reading on a fresh chain after a genesis reset.
Fix: Show "n/a" until ≥2 blocks with a positive interval exist.

**[99] Staking-vault list never displayed despite /explorer/vaults backend** — `medium` · explorer.html vs rpc/explorer.go handleExplorerVaults · observability
Impact: Per-vault data (amount, term, rate_bps, maturity height) is exposed and whitelisted but only the aggregate TVL/count renders, hiding the maturity schedule and per-vault rates.
Fix: Add a vaults widget polling get('vaults') with a maturity progress bar and empty state.

**[100] Mempool fetch failure is swallowed silently — stale data shown as live** — `medium` · explorer.html refreshMempool · observability
Impact: A catch-and-return leaves the last bubbles/KPIs/gauge frozen with no staleness indicator while the header still says "live," and the proxy's 502 detail is never surfaced.
Fix: Mark the mempool section stale (dim + note + last-updated timestamp) on failure instead of returning silently.

**[101] Difficulty card shows next-block difficulty labeled as current** — `low` · explorer.html line 262; rpc/explorer.go:59 · observability
Impact: The "Difficulty" card prints ExpectedDifficulty() (next, unmined) as if current; mid-adjustment it differs from the tip's difficulty and reinforces the same forward-looking number the hashrate card misuses.
Fix: Expose both tip and next-target difficulty, label the card "Next difficulty," or feed the hashrate card per-block difficulties for consistency.

**[102] Median fee-rate is the upper-middle element, not a true median** — `low` · mempool.go Stats · observability
Impact: For even-sized pools, rates[n/2] biases the "Median fee-rate" KPI and the pressure gauge high.
Fix: Average the two middle elements for even len, or relabel as "mid fee-rate."

**[103] Block-stream "new" highlight breaks after first paint; only index 0 ever animates** — `low` · explorer.html refreshSummary/renderStream · observability
Impact: lastHeight is set after renderStream, so every first load animates the top block as new; full innerHTML re-render means a burst of multiple new blocks animates only one.
Fix: Track previous top height separately, diff latest[] to mark all genuinely-new heights, and skip the initial-render animation.

**[104] Proxy 502/error JSON detail never reaches the user** — `low` · api/explorer.js + explorer.html get() · observability
Impact: get() throws only "HTTP <status>", so a missing NODE_RPC (500) looks identical to an unreachable node (502) with no actionable cause.
Fix: On !r.ok, read the JSON and include the error/detail field in the thrown message.

**[105] Block-search accepts non-numeric/out-of-range input and shows a raw alert()** — `low` · explorer.html search/openBlock · validation
Impact: parseInt with no validation yields NaN→400/404 surfaced as a blocking browser alert; the only navigation affordance has jarring failure UX.
Fix: Validate a non-negative integer ≤ current height before openBlock; show inline error styling and reuse the .err banner instead of alert().

---

## Top 15 to fix first

1. **[2]** Stranded first-send — split the joint key and fund/confirm OBX before prompting for XNO.
2. **[1]** Don't reveal sA until the XNO lock is cemented (call Confirmed).
3. **[3]** Validate received XNO amount against the agreed amount before settling OBX.
4. **[47]** Stop trusting a single RPC's "receivable" — confirm + cross-check.
5. **[7]/[8]** Build and actually execute the refund/abort path with timeout coordination.
6. **[9]** Compute and enforce cross-leg timelock ordering with a safety gap.
7. **[28]/[29]** Make Sweep idempotent and resumable so partial sweeps don't strand funds.
8. **[25]/[27]** Persist per-swap secrets/state to a 0600 file before advertising the joint address.
9. **[4]** Bind the locked OBX amount to the matched offer instead of hardcoded 3 OBX.
10. **[16]/[17]/[18]** Implement the real two-party take/accept + share-exchange + partial pre-signature protocol.
11. **[73]** Fix DEC.OBX=12 so OBX prices aren't 10,000× wrong.
12. **[58]** Fix the "BTC (mock)" asset value so offers aren't silently rejected.
13. **[13]/[14]** Prevent adaptor-nonce reuse (deterministic nonces + consensus ClaimR nullifier).
14. **[34]/[35]/[36]** Verify sweep completion/cementation and ensure dest actually holds spendable XNO.
15. **[42]** Widen XNO amounts to big.Int so realistic (≥1 XNO) swaps aren't truncated.