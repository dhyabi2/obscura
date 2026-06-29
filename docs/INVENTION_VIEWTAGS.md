# Invention Log — Block 9: View-Tags (fast wallet scanning)

Produced with the invention methodology + Methodology-Tree brainstorming engine.

## 1. Challenge
Wallet output scanning is the cost a light client cannot avoid: for every output
the wallet computes the ECDH shared secret (`a·R`) and then derives the one-time
key (`Hs(a·R)·G + B`) to test ownership — two scalar multiplications per output.

## 2. Brainstormed derivation (engine) + pitfalls
Engine recommendation: tag = first byte of `H(domain || a·R)` with **distinct
domain separation** from the stealth `Hs`. Top pitfalls it flagged, all heeded:
1. No domain separation → collides with the stealth derivation (key-leak risk).
2. Deriving the tag from `P` or `R` instead of `a·R` → tags recur / become
   linkable across outputs.
3. Letting the tag affect key math / skipping the full check → ~N/256 false
   positives mean the wallet must ALWAYS confirm with the full `P` check; the tag
   is a pure pre-filter.

## 3. Implementation
- `tx.Output.ViewTag` (1 byte), set by the sender: `commit.ViewTag(shared) =
  H("Obscura/view-tag" || a·R)[0]` — a label distinct from the stealth
  `"Obscura/stealth"` derivation.
- `commit.StealthKeys.ScanMatch(out, tag)`: computes `a·R` once, rejects on a tag
  mismatch (skipping the second scalar-mult for ~255/256 of non-owned outputs),
  and only then does the full ownership check. The wallet's scan path uses it.
- The tag is purely a hint: ownership is still decided by the full check, and the
  wallet additionally verifies `Commit(amount, mask) == output commitment`.

Effect: roughly halves per-output scan cost; outputs remain unlinkable (tag from
the per-output shared secret, not `P`/`R`); a wrong/garbage tag only causes the
(self-paying) sender to grief their own payment.

Tested `tests/critical/viewtag/`: sender/receiver tags agree; non-owners are
skipped; a wrong tag skips even an owned output while tag-independent `Owns`
still holds; tags differ per output (unlinkable). The end-to-end wallet/anon
suites pass with the scan path now tag-gated.
