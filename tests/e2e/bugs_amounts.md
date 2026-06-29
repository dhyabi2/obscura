# bugs_amounts.md — Obscura web wallet e2e: amount/send validation + balance formatting + multi-tab lock

Spec: `tests/e2e/c_amounts_multitab.spec.js`
Run: `cd tests/e2e && BASE_URL=http://127.0.0.1:18099 npx playwright test c_amounts_multitab.spec.js`
Result: **16 passed, 7 skipped, 0 failed** (stable across 3 consecutive runs).
Scope: UC37-54 (amount parsing / send validation + balance formatting — NO-FUNDS parts) and
UC61-65 (multi-tab lock). Pure math (`obxToAtomic`/`atomicToObx`) tested directly via
`page.evaluate`; UC44 + the lock tests drive the real UI. No funded transactions in this run.

## Real bugs found

**None.** Every assertion that exercises real wallet behavior passed against correct
expected values. No wallet-bug assertions had to be relaxed; no test-only workarounds masked a
product defect. (Two test-harness issues were fixed in the spec, not the wallet — see "Test
fixes" below.)

## Per use case

| UC | Title | Result | Notes |
|----|-------|--------|-------|
| 37 | "1" -> 1e12 atomic | PASS | `obxToAtomic("1").atomic === "1000000000000"`. |
| 38 | "0.000000000001" -> 1 atom (not zero) | PASS | sub-micro preserved, returns `"1"`. |
| 39 | >12 decimals rejected | PASS | 13-dp input -> `{error:/decimal/}`, no silent truncation. |
| 40 | "90000.000000000001" full precision | PASS | -> `"90000000000000001"`; exact round-trip via `atomicToObx`. Test also proves `Number()/1e12` WOULD collapse it (float53), documenting why BigInt is required. |
| 41 | zero / negative / "abc" rejected | PASS | `"0"`, `"-5"`, `"abc"`, `""`, `"."` all return `{error}` with a message. |
| 42 | < 1 atom blocked | PASS | `"0.000000000000"` (zero at 12dp) rejected with a clear message. |
| 43 | fee uses same BigInt parser | PASS | `"0.001"` -> `"1000000000"`; bad 13-dp fee rejected identically. Send code path calls `obxToAtomic` for both `#sendAmt` and `#sendFee`. |
| 44 | malformed send address rejected | PASS | UI: create wallet -> continue -> confirm backup -> Send enabled -> send to "garbage-not-an-address" -> `#sendMsg` shows `"bad address: base58: invalid character"`. **Graceful rejection (returned `{error}`), NO thrown exception** (zero `pageerror` events). Verified `obxBuildSend` returns `{error:"bad address: base58: invalid character"}` directly. |
| 45 | insufficient balance rejected | SKIP | FUNDED-only: needs spendable funds mined to the test address. |
| 46 | successful send -> txid / "Broadcast" | SKIP | FUNDED-only: needs funds to build + broadcast. |
| 47 | broadcast failure releases reservation | SKIP | FUNDED-only: needs funded inputs to reserve then a forced broadcast failure. |
| 48 | reserved-then-released spendable again | SKIP | FUNDED-only: needs funded outputs. |
| 49 | balance formats from BigInt (no drift) | PASS | round-trips `atomicToObx`->`obxToAtomic` exactly for `1`, `1e12`, `90000000000000001`, and `9007199254740993` (a value beyond float53 integer precision). |
| 50 | large balance (>9000 OBX) exact display | SKIP | FUNDED-only: needs a real >9000 OBX on-chain balance. Pure formatting is covered by UC49. |
| 51 | trailing-zero trim | PASS | `1500000000000`->`"1.5"`; `2000000000000`->`"2"`; `1000000000001`->`"1.000000000001"`; `1000000000010`->`"1.00000000001"` (leading fractional zeros preserved). |
| 52 | zero balance shows "0" | PASS | `atomicToObx("0") === "0"`, not NaN/undefined. |
| 53 | balance updates after sync | SKIP | FUNDED-only: needs funds + a synced height change. |
| 54 | maturity-locked vs spendable | SKIP | FUNDED-only: needs coinbase outputs at varying maturity depths. |
| 61 | single tab becomes owner | PASS | `tabIsOwner()===true`; `localStorage.obx_active_tab` slot held with an id. |
| 62 | second tab non-owner + Send disabled | PASS | 2nd page in SAME context: `tabIsOwner()===false`, `#sendBtn` disabled, `#sendMsg` shows "another tab" warning (even with backup confirmed -> shared flag, so only ownership gates it). |
| 63 | closing owner -> other takes over | PASS | after `p1.close()`, p2 reclaims ownership within the stale window (observed well under the 12s budget; `beforeunload` releases the slot), `#sendBtn` re-enabled and the warning clears. |
| 64 | no false lockout on single tab | PASS | dispatched blur/focus across >2 heartbeats; stays owner, no "another tab" title. |
| 65 | storage/BroadcastChannel re-evaluates ownership | PASS | a foreign fresh slot written from another page triggers the `storage` listener on p1, demoting it to non-owner promptly (under one TAB_BEAT, ~<2s). |

## Test fixes (harness, not wallet)

1. **`#confirmPhrase` / `#sendBtn` not visible after `createWallet` helper.** Root cause:
   `helpers.createWallet` stops on the seed-display screen; the Send box and backup gate live
   inside `#haveWallet` which stays `.hide` until `showWallet()` runs. The wallet only calls
   `showWallet()` when the user clicks "I saved it — continue". Fix (in the spec, via a local
   `createAndUnlockSend` helper): click that continue button, then fill `#confirmPhrase` +
   confirm backup. This is expected UX, not a bug.
2. **UC44 message race.** Reading `#sendMsg.innerText()` immediately after the click caught the
   transient `"building…"` muted message (set before the `await get("height")` + build). Fix:
   use a web-first assertion `await expect(#sendMsg).toContainText(/address/i)` to wait for the
   final rejection. Not a wallet bug — the final state is correct.

## Confirmations of correct, audit-relevant behavior

- Amount/fee parsing is exact fixed-point BigInt: no float53 collapse (UC40), no sub-atom
  rounding-to-zero (UC38), no silent truncation of over-precise input (UC39/43).
- Balance formatting is BigInt-based with correct trailing-zero trim and no NaN on zero
  (UC49/51/52).
- Malformed address is a handled `{error}` return, never a thrown exception (UC44).
- The single-active-tab lock elects one owner, disables Send (button + visible warning) in
  duplicate tabs, hands ownership back when the owner closes, does not false-lock a lone tab,
  and re-evaluates on storage/BroadcastChannel events (UC61-65).
