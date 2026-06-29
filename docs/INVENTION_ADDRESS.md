# Invention Log — Block 28: Base58 Checksummed Addresses

Produced with the invention methodology — adopting the proven Bitcoin/Monero
address shape (version byte + checksum + Base58) rather than inventing a format.

## The challenge
The wallet address was a raw 64-byte key pair shown as **128 hex characters**.
That is miserable to copy by hand and, worse, has **no error detection**: a single
mistyped hex character yields a different, valid-looking address and the funds are
gone. Every mainstream coin uses a compact, checksummed, human-friendly encoding.

## Chosen design
- **`pkg/base58`** — Bitcoin-style Base58 (big-integer variant). The alphabet
  omits the ambiguous `0 O I l`, so transcription errors are rarer than hex, and
  the encoding is ~25% shorter (94 vs 128 chars).
- **`commit.StealthAddress.String()` / `ParseHumanAddress`** — the human address is
  `Base58( version(1) ‖ A‖B (64) ‖ checksum(4) )`, where the checksum is the first
  4 bytes of `BLAKE2b-256(version‖A‖B)`. Decoding verifies the length, the
  **version byte** (a wrong network/format is rejected outright), and the
  **checksum** (a typo is rejected with overwhelming probability, ~1 in 2³²),
  before the keys are ever used.

The raw 64-byte `Encode`/`DecodeAddress` remain for the wire and for backward
compatibility.

## Integration
- CLI `address` and the address printed by `new` now show the Base58 form.
- `send --to` accepts **either** a Base58 address or a legacy 128-char hex address
  (`parseAddressInput` tries Base58 first, then hex), so existing scripts keep
  working while humans get the safe format.

## Tests (`tests/critical/address/`)
Base58 round-trip (200 random sizes) and leading-zero preservation, invalid-char
rejection, address round-trip (keys recovered exactly), single-character typo
rejected by the checksum, wrong-version rejected even with a valid checksum, and
garbage/empty rejected.

## Note
Unlike Monero's block-based Base58 (which guarantees a fixed leading character),
the big-integer variant doesn't pin the first character, so the version byte only
*tends* to influence the prefix. The checksum and explicit version check — not the
leading character — are what guarantee correctness.

## Future
- Block-based Base58 for a fixed, recognizable address prefix.
- Integrated/subaddress formats sharing this envelope.
