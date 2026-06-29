# Invention Log — Block 24: Encrypted Wallet at Rest

Produced with the invention methodology. (No novel crypto is warranted here —
inventing a cipher would be a footgun — so the methodology's "best existing
solution" step wins outright: a memory-hard KDF + an authenticated cipher, the
same shape Bitcoin Core and Monero use, with modern primitives.)

## The challenge
The wallet seed was stored as **plaintext hex on disk**. Anyone who read the file
— a backup, a synced folder, malware, a shared machine — owned the funds outright.
A real wallet must protect the seed behind a passphrase, such that a stolen file
is useless without it.

## Survey of best existing solutions
| Wallet | KDF | Cipher | Note |
|---|---|---|---|
| Bitcoin Core | many SHA-512 rounds | AES-256-CBC | KDF not memory-hard → GPU-friendly |
| Monero | slow hash (CN variants) | ChaCha20 | strong but bespoke |
| Modern best practice | **Argon2id** (memory-hard) | **AEAD** (XChaCha20-Poly1305 / AES-GCM) | resists GPU/ASIC; tamper-evident |

## Chosen design (`pkg/keystore`)
A self-describing encrypted blob:

```
magic "OBXKS" (5) | version (1) | salt (16) | time (4) | memKiB (4) | threads (1) | nonce (24) | ciphertext+tag
```

- **KDF: Argon2id**, key = `IDKey(passphrase, salt, time, memKiB, threads, 32)`.
  Memory-hard, so brute-forcing a stolen file is expensive on GPUs/ASICs. The salt
  and the three cost parameters are **stored in the blob**, so the cost can be
  raised in future without breaking old files (defaults: time=3, mem=64 MiB,
  threads=4 — ~80 ms on a laptop).
- **Cipher: XChaCha20-Poly1305** (AEAD). A random 24-byte nonce (XChaCha's large
  nonce makes random nonces collision-safe) and a 16-byte tag. A wrong passphrase
  or any tampering is **detected** (returns an error), never silently decrypted to
  garbage. The header (magic..threads) is passed as **associated data**, so the
  stored parameters are authenticated too.

### Hardening found while building (real bug, not just a test fix)
Storing the KDF parameters in the blob means a hostile blob could specify an
**astronomical `time`** (e.g. ~4 billion passes). Because the AEAD tag is only
checked *after* the key is derived, a victim opening such a file would be forced
into a multi-hour Argon2 computation — a denial of service. **Fix:** `Decrypt`
**bounds the parameters before running Argon2** (`time ≤ 64`, `8 KiB ≤ mem ≤
4 GiB`, `threads ≤ 64`) and rejects anything out of range immediately. A
regression test asserts a tampered-`time` blob is rejected within 5 seconds.

## Integration
- `IsEncrypted(data)` distinguishes a keystore blob from a **legacy plaintext-hex
  seed** (by the magic prefix), so old wallets keep working.
- CLI: `obscura-wallet new --passphrase PASS` (or env `OBSCURA_WALLET_PASSPHRASE`)
  writes the seed **encrypted**; without a passphrase it writes plaintext hex with
  a loud warning. Every command's `loadSeed` auto-detects an encrypted file and
  decrypts it with the passphrase (env preferred over the flag, so it need not
  appear in shell history). Missing/wrong passphrase fails cleanly.

## Tests (`tests/critical/keystore/`)
Round-trip (and that the recovered seed derives the same wallet), plaintext seed
absent from ciphertext, wrong-passphrase rejection, tamper detection (ciphertext
and salt), DoS-bound on tampered KDF params, unique ciphertexts (random
salt/nonce), and legacy-plaintext is not misdetected. Tests lower the KDF cost via
the exported params so the suite stays fast; production defaults stay memory-hard.

## Block 25 — Encrypt the scan state + change-passphrase
Encrypting only the seed was half a fix: the `<wallet>.state` file holds every
owned output's **amount AND one-time secret key**, so a plaintext state leaks both
balances and spend authority. Now the state is protected too, and the passphrase
can be set/rotated.

- **Encrypted state.** The CLI's `loadState`/`saveState` helpers transparently
  decrypt/encrypt the state with the wallet passphrase via the same keystore
  primitive: if a passphrase is available the state is sealed, otherwise it stays
  plaintext (matching the wallet's own choice). Writes are **atomic** (temp file +
  rename) so an interrupted save can't corrupt existing state. A passphrase-
  protected wallet already requires the passphrase to load the seed, so the state
  always has it available.
- **`passwd` command.** `obscura-wallet passwd [--passphrase OLD] --new-passphrase
  NEW` decrypts with the old passphrase (or reads a plaintext wallet) and
  re-encrypts **both the seed and the scan state** with the new one — so it
  doubles as "encrypt an existing plaintext wallet." Verified end-to-end:
  encrypt-a-plaintext-wallet, rotate, old passphrase then fails, new preserves the
  address.

Test `tests/critical/walletstate/TestEncryptedStateRoundTrip`: the encrypted state
contains no plaintext, won't open with the wrong passphrase, and decrypts back to
a wallet with the same balance and last-scanned height.

## Future
- Interactive passphrase prompt (no-echo TTY) instead of flag/env.
