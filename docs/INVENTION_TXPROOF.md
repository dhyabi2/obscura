# Invention Log — Block 27: Payment (Receipt) Proofs

Produced with the invention methodology — adopting the proven Monero
`get_tx_proof`/`check_tx_proof` design (a Chaum-Pedersen DLEQ over the ECDH shared
secret) rather than inventing anything new.

## The challenge
A privacy coin hides who paid whom. But sometimes a party WANTS to prove a
specific payment — a merchant proving a customer paid, a user proving to an
exchange/auditor that an address received funds — **without** handing over their
view/spend keys (which would expose their entire history) and **without** linking
their other outputs.

## Chosen design (`pkg/commit`)
The recipient holds view secret `a` (with `A = a·G`) and an on-chain output
`(P, R)` where `R` is the transaction pubkey and `P = Hs(a·R)·G + B`. To prove the
output is a payment to their address `(A, B)`:

1. Publish the **ECDH shared point** `D = a·R` (this equals `r·A`, the value the
   sender used to derive the output).
2. Attach a **Chaum-Pedersen DLEQ** (`pkg/commit/dleq.go`) proving that the *same*
   secret `a` satisfies `A = a·G` **and** `D = a·R` — i.e. `D` really is this
   address's ECDH secret for `R`, not an arbitrary point. The proof reveals
   nothing about `a` (zero-knowledge Schnorr: `T1=k·G, T2=k·R, c=H(…), s=k+c·a`).

**Verification** (`VerifyPayment`): check the DLEQ, then confirm
`Hs(D)·G + B == P` (so `D` genuinely produces this output's one-time key for
`(A,B)`), and finally **decrypt the amount** with `D` as the shared secret. The
verifier learns *only* that this one output paid `(A,B)` and how much — not `a`,
not `b`, and nothing about any other output.

### Why it's sound
- DLEQ binds `D` to the address's view key, so a third party can't fabricate a
  `D` that passes the `Hs(D)·G+B == P` check for an output they don't control.
- A non-recipient has no `a`, so cannot produce a DLEQ linking `A` to any `D=a·R`;
  tests confirm a forged proof fails for both the attacker's own address and the
  real address.
- The amount is taken from the on-chain encrypted field decrypted with `D`, so the
  prover can't lie about it.

## What is built
- `pkg/commit/dleq.go` — `ProveDLEQ`/`VerifyDLEQ` (Chaum-Pedersen), `BasePoint`,
  serialize/parse. A reusable same-curve DLEQ primitive.
- `pkg/commit/txproof.go` — `PaymentProof{D, DLEQ}`, `StealthKeys.ProveReceipt`,
  `VerifyPayment` (returns the amount), 96-byte serialize/parse.
- `wallet.ProvePayment(ownedOutput)` — produce a receipt proof for a scanned
  output (needs a fresh scan: the persisted state omits the tx pubkey).

## Tests (`tests/critical/txproof/`)
Valid proof reveals the correct amount; verification fails for the wrong address;
a non-recipient cannot forge (for their own or the real address); tampered `D`/`S`
rejected; 96-byte serialize round-trip; DLEQ soundness (mismatched discrete logs
and wrong domain rejected); and an end-to-end wallet proof on a real chain.

## Block 29 — Self-contained bundle + CLI
A verifier shouldn't need a node or the wallet to check a proof. `ReceiptBundle`
packages everything: the output's `P`, `R`, encrypted amount, and the proof
(`D` + DLEQ) — 168 bytes, hex-encoded. `VerifyBundle(addr, bundle)` checks it
offline and returns the amount.

- `wallet.OwnedOutput.SourceTx` (the creating txid, set on scan, not persisted) +
  `wallet.ProvePaymentBundle` lets the wallet produce a bundle for an output.
- CLI: `obscura-wallet proof --txid T` prints a bundle for each output the wallet
  received in tx `T`; `obscura-wallet checkproof --address ADDR --proof HEX`
  verifies it **with no node**, printing `VALID: that address received N OBX` or
  `INVALID`. Verified end-to-end (correct address → VALID + amount; wrong address
  → INVALID). Library test: `TestReceiptBundleRoundTrip`.

## Block 31 — Sender (out) proof: "prove I paid you"
The symmetric direction. The payer knows the per-output tx secret `r` (with
`R = r·G`); they prove the payment by publishing `D = r·A` and a DLEQ that the same
`r` satisfies `R = r·G` and `D = r·A`. Crucially, this is verified by the **same**
`VerifyPayment`: it now accepts **either** orientation —
`VerifyDLEQ(G,A,R,D)` (recipient knows `a`) **or** `VerifyDLEQ(G,R,A,D)` (sender
knows `r`) — followed by the same `Hs(D)·G+B == P` and amount checks. Either party
attests the same fact, and soundness is unchanged (each orientation independently
requires a real witness: `a` or `r`).

- `wallet` now retains `r` for the destination output of every spend (stashed by
  txid at build time in `buildSpend`/`buildOutputR`, persisted as `SentTx.DestR`).
- `commit.ProveSpend(r, addr)` builds the out-proof; `wallet.ProveSpendBundle(sent)`
  packages it from a recorded payment.
- CLI `obscura-wallet proofsent --txid T` prints a bundle; the payee verifies it
  with the existing `checkproof --address <their address> --proof HEX`.

Tests: `TestSenderOutProof` (lib), `TestWalletSenderProofEndToEnd` (wallet on a
real chain — proof verifies for the recipient, fails for an unrelated address).

## Future
- CLI niceties (combined receive/spend proof listing).
