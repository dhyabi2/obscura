# Invention Log — Block 30: Subaddresses (Deterministic Sub-Accounts)

Produced with the invention methodology. Monero's subaddress scheme is the
reference; we adopt its *goal* (many unlinkable receive addresses from one seed)
but choose a derivation that needs **no sender or consensus changes**.

## The challenge
Reusing one address for every payment lets observers cluster all of a user's
incoming funds. The fix is a fresh address per invoice/customer — but all
controlled by the same seed, all scannable and spendable by one wallet, and
**mutually unlinkable** on-chain.

## Monero's approach and why we diverge
Monero subaddress `(i)` is `D_i = B + Hs(a‖i)·G`, `C_i = a·D_i`, and crucially the
**sender** must publish `R = r·D_i` (the subaddress spend key as the base) so the
receiver can detect it. That keeps scanning O(1) regardless of subaddress count,
but it changes the transaction-construction rules — every sender and the consensus
output format must understand subaddresses.

For a clean, low-risk increment we instead make each subaddress an **independent
keypair** derived from the master secrets:

```
a_i = H("Obscura/sub-view",  a, i)      (fresh view secret)
b_i = H("Obscura/sub-spend", b, i)      (fresh spend secret)
A_i = a_i·G,  B_i = b_i·G                (index 0 = the main account)
```

These are ordinary `(A_i, B_i)` addresses, so payments use the **existing** output
format (`R = r·G`) — **zero** changes to senders, transactions, or consensus. They
share no algebraic relation visible without the master secrets, so distinct
subaddresses (and the main address) are mutually unlinkable.

**Trade-off** (documented honestly): the wallet must try each output against all
its subaddress keys, so scanning is O(#subaddresses) ECDH per output rather than
Monero's O(1). The view-tag pre-filter still rejects ~255/256 of non-owned outputs
cheaply per key, so for a reasonable number of subaddresses this is fine; a future
block could adopt the Monero `R = r·D` scheme for O(1) scanning if needed.

## What is built
- `commit.StealthKeys.Subaddress(index)` — derive sub-account keypair (deterministic,
  independent, index 0 = main).
- `wallet`: `subCount`, `subKeys()`, `SubaddressCount`, `SubaddressAt`,
  `NewSubaddress()`. Scanning (`tryClaim`) tries the main account + every
  subaddress and records which one owns each output (so the right one-time secret
  is stored for spending). `subCount` is persisted in the wallet state (appended
  after the sent section; older state files restore with 0).
- CLI `obscura-wallet subaddress` generates the next subaddress and persists the
  count so it is scanned thereafter.

Spending is unchanged: an output received on a subaddress stores its one-time
secret like any other, so the normal transaction builder spends it; change returns
to the main account.

## Tests (`tests/critical/subaddress/`)
Derivation is distinct/deterministic and never collides with the main address;
a payment to a subaddress is detected and combines with the main balance; a
subaddress-received output is spendable; and the subaddress count + balance
survive a state round-trip.

## Future
- O(1) scanning via the Monero `R = r·D` construction (sender-aware subaddresses).
- Accounts/index hierarchy and per-subaddress labels in the wallet.
