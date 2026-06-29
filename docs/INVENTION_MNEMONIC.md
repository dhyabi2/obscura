# Invention Log — Block 26: Mnemonic Seed Phrase

Produced with the invention methodology. As with the keystore, inventing new
cryptography here would be a footgun; the win is adopting the proven *encoding*
(BIP39's entropy↔words scheme) with a self-contained wordlist.

## The challenge
A seed is 32 bytes / 64 hex characters. Writing that down by hand and reading it
back is error-prone, and a single wrong character silently produces a different,
empty wallet. Every serious wallet offers a **word-phrase backup**: easier to
transcribe, and checksummed so typos are caught.

## Survey
- **BIP39** (Bitcoin/Ethereum): entropy ‖ checksum, split into 11-bit groups
  indexing a 2048-word list; 256-bit entropy → 24 words. The de-facto standard.
- **Monero (Electrum-style)**: 25 words over a 1626-word list with a CRC32
  checksum word.

Both are sound. BIP39's entropy encoding is the simplest to implement correctly
and maps cleanly onto our 32-byte seed (256 + 8 checksum bits = 264 = 24 × 11).

## Chosen design (`pkg/mnemonic`)
The **BIP39 entropy encoding**, with an **Obscura-specific wordlist**:

- **Encoding.** `checksum = first (entropyBits/32) bits of SHA-256(entropy)`;
  `entropy ‖ checksum` is split into 11-bit groups, each indexing the wordlist. A
  32-byte seed → 8 checksum bits → 264 bits → **24 words**. (16–32 byte entropy in
  4-byte steps is supported.)
- **Decoding** reverses it and **verifies the checksum**, so a single mistyped
  word fails (≈255/256 chance per word) rather than returning a wrong-but-valid
  seed. Unknown words and bad word counts are rejected explicitly.
- **Wordlist.** 2048 entries generated deterministically as `C V C V` syllables
  over 16 consonants × 5 vowels (6400 combos, first 2048 taken) — pronounceable,
  unique by construction, and embedded with **zero data files / dependencies**.
  (e.g. `gabe hano gima bori …`.) This is **not** BIP39-English-compatible; it is a
  backup encoding for *this* wallet. The cryptographic seed is unchanged — this is
  purely an encoding of it, so it composes with the keystore (a restored seed can
  then be re-encrypted with a passphrase).

## Integration
- CLI `obscura-wallet mnemonic` prints the phrase for the current wallet (decrypts
  first if the wallet is encrypted).
- CLI `obscura-wallet restore --mnemonic "words…"` (or env
  `OBSCURA_WALLET_MNEMONIC`) recreates the seed file from a phrase, encrypting it
  if a passphrase is supplied — sharing the `new` write path (refuses to clobber
  an existing wallet). `new` now also points users at `mnemonic` for backup.

Verified end-to-end: `new` → `mnemonic` (24 words) → `restore` into a fresh,
encrypted file reproduces the **same address**; a typo'd word is rejected.

## Tests (`tests/critical/mnemonic/`)
50× random round-trip, decoded-seed-derives-same-wallet, checksum typo detection,
unknown-word rejection, bad word-count rejection, wordlist is 2048 & unique, and
bad-entropy rejection.

## Future
- Optional BIP39-English wordlist for cross-wallet interoperability.
- A passphrase ("25th word") mixed into seed derivation for plausible deniability.
