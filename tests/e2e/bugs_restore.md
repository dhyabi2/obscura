# Restore + Backup-Gate e2e results (UC23–36)

Spec: `tests/e2e/b_restore_backup.spec.js`
Run:  `cd tests/e2e && BASE_URL=http://127.0.0.1:18099 npx playwright test b_restore_backup.spec.js`
Target: live wallet at http://127.0.0.1:18099 (node already running; not managed by these tests).
Result: **14/14 tests PASS** (verified stable across 2 consecutive runs).

## (a) Per-use-case status

### C. Backup-confirm gate
| UC | Description | Status |
|----|-------------|--------|
| 23 | Send disabled until backup confirmed | PASS |
| 24 | Wrong backup phrase rejected (error, flag not set, send still disabled) | PASS |
| 25 | Correct 24 words set `obx_backup_confirmed`=1 + enable send | PASS |
| 26 | Backup flag persists across reload (and after passphrase unlock) | PASS |
| 27 | `createWallet()` clears a stale backup-confirmed flag | PASS |
| 28 | Restore sets backup-confirmed (re-typing phrase proves possession) | PASS |

### D. Restore + address confirmation
| UC | Description | Status |
|----|-------------|--------|
| 29 | Restore accepts a valid 24-word mnemonic (derived addr shown) | PASS |
| 30 | Restore accepts a valid 12-word mnemonic | PASS |
| 31 | Restore rejects 13- and 23-word phrases (count error, no derived addr) | PASS |
| 32 | Derived address shown; confirm required before wallet stored/opened | PASS |
| 33 | Cancel leaves wallet unstored + re-prompt works | PASS |
| 34 | 12-vs-24 (23-word) mismatch never silently opens a wallet | PASS |
| 35 | Whitespace/case normalized → same derived address | PASS |
| 36 | Restored wallet derives a deterministic, stable address | PASS |

No SKIPs. (UC30: see note below — the 12-word case is fully exercised, not skipped.)

## (b) Bugs found

**None.** Every assertion against correct behavior passed. The wallet behaves
exactly as the code intends:
- Send is gated behind `obx_backup_confirmed`; `confirmBackup()` compares the
  re-typed phrase against the in-memory `_seed`, not anything in storage.
- `restoreWallet()` enforces an exact 12-or-24 word count *before* deriving, so a
  12↔24 paste mismatch (e.g. 23 words) is rejected with a clear message and never
  silently derives a different wallet (audit #5 guard works).
- The derived address is shown and `confirmRestore()` is required before the seed
  is encrypted to localStorage; cancelling stores nothing.
- The seed is only ever persisted as the encrypted `obx_mnemonic_enc` blob; the
  plaintext `obx_mnemonic` key was never observed.
- `normPhrase()` (trim + lowercase + whitespace-collapse) makes restore
  case/whitespace-insensitive and address derivation deterministic.

## Notes / harness observations (not bugs)

1. **12-word vector source (UC30).** `obxGenerate()` always yields a 24-word
   (256-bit) seed — there is no UI/WASM path to produce 12 words. The 12-word case
   uses a valid vector (`VEC12`) computed once in Node by replicating the wallet's
   own mnemonic codec (BIP39 entropy encoding over Obscura's deterministic CVCV
   wordlist) from 16-byte entropy `00112233445566778899aabbccddeeff`. The test
   first asserts `obxRestore(VEC12)` decodes without error (proving the vector is
   genuinely valid for this wallet), then drives the full UI restore flow. This is
   a fully-exercised case, not a skip.

2. **UC27 phrasing.** Once a wallet is open, the "Create new wallet" button lives
   on the hidden `#noWallet` card, so a literal "create over an open wallet" is not
   a reachable UI flow. The test instead proves the load-bearing guarantee: with a
   stale `obx_backup_confirmed=1` left in storage, clicking `createWallet()` wipes
   it (wallet.html line 543: `localStorage.removeItem("obx_backup_confirmed")`),
   forcing re-confirmation for the new seed.

3. The 24-word phrases shown after create render inside `#newSeed .seedbox`; the
   restore-derived address renders in `#restoreDerivedAddr`. Both were read via
   real DOM text, and gate/flag state via real `localStorage`.
