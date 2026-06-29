# Invention Log — Block 10: Optional Tor Transport

Produced with the invention methodology + Methodology-Tree brainstorming engine.

## 1. Challenge
Sender anonymity (crypto) and Dandelion++ (tx-origin) still leave the node's IP
visible to peers and observers. Add optional Tor so node-to-node traffic — and
thus node identity — is hidden, without cgo.

## 2. Brainstormed design + pitfalls (engine)
Recommended: SOCKS5 outbound via the local Tor proxy (`x/net/proxy`), inbound
via an onion hidden service, advertise the `.onion` as the peer id. Top pitfalls,
all addressed:
1. **IP/DNS leaks** — stdlib `net.Dial` bypasses the custom dialer. We route ALL
   outbound through an injected `Dialer`, and the SOCKS5 dialer lets Tor resolve
   names (no local DNS).
2. **Clearnet fallback mixes anonymity sets** — fail closed: Tor mode never falls
   back to clearnet and never stores/relays clearnet peer addresses.
3. **Ephemeral onion rotation** — the onion key must be persistent; the operator
   supplies a stable `.onion` (the node takes it as a flag; auto-creation
   via the Tor control port `ADD_ONION` is a documented next step).

## 3. Implementation
- `pkg/p2p/transport.go`: a `Dialer` interface; `clearnetDialer` (default) and
  `torDialer` (`NewTorDialer(socksAddr)` over `proxy.SOCKS5`). `Node.SetTransport
  (dialer, advertiseAddr, onionOnly)` selects the transport, the advertised
  address (the `.onion`), and onion-only mode.
- All outbound dialing goes through `n.dialer.Dial`; the handshake advertises
  `n.advertiseAddr`; in onion-only mode `maybeAddAddr` drops clearnet addresses
  from PEX (fail closed).
- Node flags `--tor-proxy` and `--onion-address` wire it on the CLI.

Tested: `tests/critical/tor/` — outbound goes through an injected dialer and the
node still syncs (transport is genuinely pluggable, the Tor hook); a node's
advertised `.onion` propagates to peers. `pkg/p2p` internal tests cover the
onion-only PEX filter and `isOnion`. No Tor daemon needed for tests.

## 4. Operator note
To actually run over Tor: start `tor`, create a hidden service forwarding the
onion port to the node's local P2P listener, then run with
`--tor-proxy 127.0.0.1:9050 --onion-address <yourhash>.onion:<port>`.
