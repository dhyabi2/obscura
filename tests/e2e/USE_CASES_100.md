# Obscura Web Wallet — 100 e2e Use Cases (Playwright)

Harness: `obscura-node --ui` serves the embedded wallet (synced from `website/`) on `127.0.0.1:18099`
with its `/api/explorer` proxy to the node RPC. Tests in `tests/e2e/*.spec.js`. Real-XNO swap cases
use ≤ 0.00001 XNO and require `OBX_NANO_*` env (skipped if absent). Funded cases mine to a fixed
test address (coinbase-maturity=1). Bugs recorded in `BUGS_FOUND.md`.

Status legend: [A]=automated here · [P]=planned (needs funds/XNO/manual) · result tracked in results.json.

## A. Load / integrity / security headers (1–10)
1. wallet.html returns 200 and correct title. [A]
2. wallet.wasm served as application/wasm. [A]
3. WASM passes the in-page SHA-384 integrity check and initializes (all obx* fns present). [A]
4. Tampered wasm (wrong byte) is rejected before instantiate (served variant / hash mismatch path). [P]
5. wasm_exec.js carries the SRI integrity attribute. [A]
6. CSP meta present with script-src 'self' 'wasm-unsafe-eval'. [A]
7. CSP blocks an injected external script (connect/script-src 'self'). [A]
8. Visible build-hash footer matches the served wasm. [A]
9. No unexpected console errors on load (frame-ancestors-in-meta warning is known/acceptable). [A]
10. obxReady readiness signal does not precede function availability (race check). [A]

## B. Wallet creation + at-rest encryption (11–22)
11. "Create wallet" prompts for a passphrase (with confirm). [A]
12. Passphrase < min length is rejected. [A]
13. Mismatched passphrase confirm is rejected. [A]
14. On create, a 24-word mnemonic is shown with a back-up warning. [A]
15. localStorage stores obx_mnemonic_enc (encrypted blob), never plaintext obx_mnemonic. [A]
16. Encrypted blob has {v,kdf,iters,salt,iv,ct} with iters≥200k. [A]
17. Reload prompts for passphrase to unlock (no plaintext seed in storage). [A]
18. Correct passphrase unlocks and restores the same address. [A]
19. Wrong passphrase shows an error and allows retry (no lockout loop). [A]
20. "Forget wallet" clears the encrypted blob + state. [A]
21. Legacy plaintext obx_mnemonic is migrated to encrypted on next unlock + plaintext removed. [A]
22. Passphrase-vs-recovery-phrase safety warning is shown (passphrase not recoverable). [A]

## C. Backup-confirm gate (23–28)
23. Send is disabled until backup is confirmed. [A]
24. Confirming with the wrong phrase is rejected. [A]
25. Confirming with the correct 24 words sets obx_backup_confirmed and enables send. [A]
26. Backup flag persists across reload. [A]
27. Creating a new wallet clears the backup-confirmed flag. [A]
28. Restore sets backup-confirmed (typing the phrase proves possession). [A]

## D. Restore + address confirmation (29–36)
29. Restore accepts a valid 24-word mnemonic. [A]
30. Restore accepts a valid 12-word mnemonic. [A]
31. Restore rejects an unexpected word count (e.g. 13/23). [A]
32. Restore shows the derived address and requires "this is my address" confirm. [A]
33. Cancelling the address confirm re-prompts (wallet not stored). [A]
34. A 12-vs-24 mismatch never silently derives a different wallet (count-validated). [A]
35. Whitespace/case-insensitive phrase entry normalizes correctly. [A]
36. Restored wallet derives a deterministic, stable address. [A]

## E. Amount parsing / send validation — BigInt (37–48)
37. Amount "1" -> 1e12 atomic units. [A]
38. Sub-micro "0.000000000001" (1 atom) accepted, not rounded to zero. [A]
39. Amount with >12 decimals is rejected (no silent truncation to zero). [A]
40. Large amount (e.g. 90000.000000000001) keeps full precision (no float53 collapse). [A]
41. Zero / negative / non-numeric amount rejected with a clear message. [A]
42. Send blocked when amount resolves to < 1 atom. [A]
43. Fee field uses the same BigInt parser. [A]
44. Send to a malformed address is rejected. [A]
45. Send with insufficient balance is rejected (funded). [P]
46. Successful send returns a txid and shows "Broadcast ✓". [P]
47. Broadcast failure releases the input reservation (retry works, no false "insufficient funds"). [P]
48. Reserved-then-released outputs are spendable again. [P]

## F. Balance display — BigInt formatting (49–54)
49. Balance formats from BigInt, not Number()/1e12. [A]
50. Large balance (>9000 OBX) displays exactly (no float drift). [P]
51. 12-decimal fractional balance trims trailing zeros correctly. [A]
52. Zero balance shows 0, not NaN/undefined. [A]
53. Balance updates after sync to the funded height. [P]
54. Coinbase-maturity-locked vs spendable balance distinguished. [P]

## G. Reorg recovery (55–60)
55. obxScanUndo(fromHeight) un-spends orphaned spends. [A] (unit-level via exposed fn)
56. obxScanUndo drops outputs received in orphaned blocks. [A]
57. Auto reorg detection: changed node-hash at lastScanned triggers walk-back + undo. [P]
58. Manual "Re-sync (fix balance after reorg)" button calls obxScanUndo + re-scans. [A]
59. obxScanUndo is not called during a normal (no-reorg) sync. [P]
60. Balance is correct after a simulated reorg + re-scan. [P]

## H. Multi-tab lock (61–65)
61. A single tab becomes the active owner (heartbeat). [A]
62. A second tab sees "wallet open in another tab" and Send is disabled. [A]
63. Closing the owner tab lets another tab take over within the stale timeout. [A]
64. Heartbeat survives transient focus changes (no false lockout). [A]
65. BroadcastChannel/storage event triggers immediate re-evaluation. [A]

## I. Trust/privacy disclosure (66–70)
66. Trust-disclosure banner is shown on the hosted page. [A]
67. Banner wording reflects encrypted (not plaintext) seed. [A]
68. "Run your own node for full privacy" guidance is present. [A]
69. own-node note hidden when served from localhost (auto-soften). [A] (served locally here)
70. No operator Nano address exposed via the wallet page/proxy. [A]

## J. Confidential vaults (71–80)
71. Vault deposit form validates amount (BigInt) + term. [A]
72. Vault deposit builds a tx (obxBuildVaultDeposit) and returns a vault_id. [P]
73. Unknown vault term is rejected. [A]
74. Vault claim form requires a vault id. [A]
75. Vault claim builds a tx (obxBuildVaultClaim). [P]
76. Claim before maturity is rejected/disabled. [P]
77. Vault yield rate displayed for the chosen term. [A]
78. Vault deposit reserves inputs; failure releases them. [P]
79. Vault list shows active deposits after sync. [P]
80. Matured vault shows claimable status. [P]

## K. Cross-chain swaps OBX<->XNO (81–92) — real XNO ≤ 0.00001
81. Swap UI lists available offers from the order book. [A]
82. Offer shows give/get assets + amounts (not just names). [A]
83. Build an OBX->XNO offer (obxBuildOffer) with valid params. [A]
84. Offer amount uses BigInt parsing (no rounding). [A]
85. Public swap-take is gated/refused on an untrusted node (OBX_PUBLIC_SWAPS off). [A]
86. Take dialog shows amounts before confirm. [A]
87. Real XNO lock of 0.00001 succeeds against the configured Nano RPC. [P]
88. Maker sweeps the locked XNO after the OBX claim. [P]
89. Stalled swap state persists (resumable) — no silent in-memory fallback. [A] (startup gate)
90. Refund path triggers after the timelock on an aborted swap. [P]
91. XNO secret is never sent over the public proxy. [A] (proxy whitelist)
92. Swap fee shown matches the maker fee. [A]

## L. Explorer / sync / misc (93–100)
93. Wallet syncs from the node (height advances, no stall). [A]
94. Sync handles an empty chain (height 0) without error. [A]
95. Export/import wallet scan state round-trips (obxExportState/Import). [A]
96. Receive: wallet shows a copyable address + QR (if present). [A]
97. Subaddress generation works (obxInfo subaddress) if exposed. [P]
98. Theme/dark-mode + responsive layout render without overflow. [A]
99. Network error (node down) shows a graceful message, not a crash. [A]
100. Page has no uncaught exceptions across the full create->backup->send flow. [A]
