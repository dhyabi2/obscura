# Invention Log — Block 32: View-Only (Watch) Wallets

Produced with the invention methodology — adopting Monero's view-key model, which
falls straight out of the dual-key stealth scheme already in use.

## The challenge
Auditors, exchanges, accountants, and point-of-sale terminals need to **see**
incoming payments and balances without the power to **spend**. Handing over the
full wallet is unacceptable; the answer is a key that grants visibility only.

## Why it's natural here
The stealth scheme already splits keys: the **view secret `a`** is what detects
incoming outputs (`Hs(a·R)·G + B == P`) and decrypts amounts, while the **spend
secret `b`** is only needed to derive the one-time spend secret `x = Hs(a·R) + b`.
So `(a, B)` — view secret plus the *public* spend key — is exactly a watch
capability.

## What is built
- `commit.StealthKeys`: `b` may now be nil (watch-only). `IsViewOnly()`,
  `ViewKey()` (exports `a‖B`, 64 bytes), `StealthKeysFromViewKey`. The
  spend-dependent paths are guarded: `OneTimeSecret` errors for view-only, and
  `Subaddress` returns the main account (subaddress spend keys need `b`).
- `wallet`: `FromViewKey`, `ViewKey`, `IsViewOnly`. Scanning still **detects and
  values** outputs for a watch wallet (recording them with a nil one-time secret),
  so balances are correct; the four spend entry points
  (`CreateTransaction`, `BumpFee`, `CreateAnonTransaction`, `FundSwap`) refuse with
  "view-only wallet cannot spend."
- CLI: `obscura-wallet viewkey` exports the key; `obscura-wallet watch --viewkey
  HEX` creates a watch-only wallet file (`OBXVIEW1:` prefix; `loadWallet`
  auto-detects it). Verified end-to-end: the watch wallet shares the full wallet's
  address, reports the same balance, and refuses to send.

## Limitations (documented)
- Watch-only covers the **main account** only. Our subaddresses derive their spend
  secret from `b` (independent keypairs), which a view-only wallet lacks, so it
  can't scan subaddresses. A future block could switch to the Monero
  `D_i = B + Hs(a‖i)·G` construction so a view key scans all subaddresses.

## Tests (`tests/critical/viewonly/`)
Watch wallet shares the address and balance with the full wallet, refuses to spend
while the full wallet still can, view-key round-trip recovers the address, a
view-only key cannot derive a spend secret, and a malformed view key is rejected.
