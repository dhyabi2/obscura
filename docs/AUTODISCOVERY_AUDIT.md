# Obscura (OBX) — Peer Auto-Discovery / Bootstrapping Security Audit

**Scope:** Peer/node auto-discovery and bootstrapping.
**Primary code reviewed:** `pkg/p2p/p2p.go`, `cmd/obscura-node/main.go`, `pkg/config/params.go`.
**Date:** 2026-06-23
**Method:** Static read-only review. No `.go` / `pkg/` / `cmd/` files were modified.

## Current State (ground truth from the code)

- Peers come **only** from a manual `--seeds` flag (`cmd/obscura-node/main.go:33,55-58`). Default is the empty string → zero peers.
- `Node.Start(seeds)` spawns one `go n.connect(s)` goroutine per seed (`pkg/p2p/p2p.go:54-65`).
- `connect()` is an infinite loop: `DialTimeout(5s)` → on failure `Sleep(5s); continue`; on success `handle(conn)` then `Sleep(5s)` and retry (`p2p.go:77-87`). No backoff, no jitter, no give-up, no cap.
- `handle()` registers the peer in `n.peers` keyed by `conn.RemoteAddr().String()` (`p2p.go:89-99`).
- Handshake is `msgHello`(height) + `msgGetTip` (`p2p.go:101-103`). **No magic, no version, no services, no user-agent, no nonce.** `msgHello` payload is ignored (`p2p.go:119-120`).
- Message set: `msgHello, msgTip, msgGetTip, msgGetBlk, msgBlock, msgTx` (`p2p.go:22-29`). **No `getaddr`/`addr`.**
- No peerstore, no persistence, no bucketing, no banning, no eviction, no NAT traversal, no Tor, no Dandelion++, no max/min peer targets.
- `config.NetworkSeed = "obscura-mainnet-v1"` exists but is used only for genesis derivation — **never sent on the wire** as a magic/network id.
- Default P2P port `18080` is hardcoded (`config.DefaultP2PPort`).

---

## 100 Findings

`#N | severity | area | issue | failure/attack scenario | recommended fix`

1 | critical | bootstrap | Default `--seeds` is empty (`main.go:33,56`); a node started without it spawns zero `connect()` goroutines and never finds a peer | First-time/naive operators run a fully isolated node that silently never syncs | Ship a hardcoded default seed set so an unflagged node still bootstraps
2 | critical | bootstrap | No hardcoded seed list anywhere in the binary | Network has no built-in entry point; every node depends on out-of-band seed sharing | Embed a curated, signed list of `host:port` seeds in `pkg/config`
3 | critical | bootstrap | No DNS seeding (no DNS-seed resolver, no message type, no config) | Cannot dynamically advertise live seed IPs; operators must hand-edit flags as seeds churn | Add DNS seeders returning A/AAAA records, resolved at startup and periodically
4 | critical | bootstrap | Single point of failure: with one seed, that host's downtime partitions all new nodes | One seed reboot/outage stops the entire network's growth | Require/ship ≥6 diverse seeds and DNS seeders across operators/hosting
5 | critical | bootstrap | Dead `--seeds` entry → `connect()` loops forever dialing it every 5s with no fallback (`p2p.go:79-82`) | A stale seed IP burns a goroutine forever and the node never compensates by trying others | Add fallback to DNS/hardcoded seeds when seed dials keep failing
6 | high | bootstrap | No bootstrap fallback ordering (manual → DNS → hardcoded → peerstore) | Any single failed layer is terminal because no other layer exists | Implement a layered bootstrap pipeline with the peerstore as warm-start
7 | high | bootstrap | Bootstrap is operator-centralized: whoever controls the seed list controls who joins | Seed operator can censor/segment the network by handing out a partitioned view | Decentralize via DNS seeds + PEX + persistent peerstore so seeds matter only once
8 | high | bootstrap | No authenticity check on seeds — any `host:port` is trusted implicitly | MITM/DNS spoof feeds a victim attacker-controlled seeds (eclipse from boot) | Pin seeds, prefer signed seed manifests; treat seeds as untrusted addr sources only
9 | high | bootstrap | No minimum-peer target; node is "satisfied" with one seed connection | A node that reaches just the attacker's seed never seeks diversity | Maintain a target outbound count (e.g. 8) and keep filling slots
10 | high | bootstrap | Seeds are never "released" after bootstrap (Bitcoin disconnects seeds once enough addrs learned) | Long-lived dependence on/exposure to bootstrap hosts | After learning enough addresses, drop seed-only connections
11 | critical | PEX | No `addr`/`getaddr` messages exist (`p2p.go:22-29`) | A node can never learn any peer beyond its seeds; the address graph cannot expand | Add `getaddr`/`addr` PEX messages and request addrs after handshake
12 | critical | PEX | No mechanism to learn new peers → network cannot grow organically | Adding the 100th node requires manually seeding it with existing nodes' IPs | Implement gossip-based address propagation
13 | critical | PEX | Network partitions cannot self-heal (no addr relay to bridge sub-graphs) | A transient split into two components stays split permanently | Periodic addr exchange lets components rediscover each other
14 | high | PEX | No periodic self-advertisement of own reachable address | Inbound-reachable nodes are never discovered by the rest of the network | Periodically relay own `addr` (with timestamp) to a few peers
15 | high | PEX | When PEX is added there are no addr-flood controls (rate limit, dedupe, per-peer cap) | Future addr spam DoS / table poisoning | Design PEX with per-peer addr-rate limits and bounded relay fan-out (2 peers)
16 | high | PEX | No timestamp on addresses (none exist), so no freshness/aging when PEX is added | Cannot distinguish live vs. dead peers; stale addrs accumulate | Carry `lastSeen` timestamps in addr records and age them
17 | high | PEX | No cap on `addr` message size/count (none exists) | One `addr` message could carry millions of entries (memory DoS) | Cap `addr` to ~1000 entries and reject oversized messages
18 | med | PEX | No deterministic dedup of learned addresses | Same address re-learned repeatedly wastes table space | Key the address table by `IP:port` with dedup
19 | critical | eclipse | All peer slots filled from one source (the seed flag); no peer diversity enforcement | Attacker who is your only seed owns 100% of your view (full eclipse) | Enforce diversity from multiple independent address sources
20 | critical | eclipse | No per-IP / per-/16 / per-ASN diversity limits on connections | Attacker on one /24 fills every slot; victim sees only attacker | Limit outbound connections per /16 (IPv4) and per /32-group (IPv6)
21 | critical | eclipse | No new/tried table separation (Bitcoin-style) | Flooding cheap addresses instantly dominates peer selection (trivial poisoning) | Implement separate "new" and "tried" address buckets
22 | critical | eclipse | No anchor connections persisted across restart | Restart re-runs bootstrap from scratch → attacker re-eclipses every restart | Persist 2 anchor peers and reconnect them first on boot
23 | critical | eclipse | No inbound/outbound separation in `n.peers` (single map, `p2p.go:38`) | Attacker opens many inbound conns and they're treated like outbound for gossip; eclipse via inbound flooding | Track and separate inbound vs outbound; never count inbound toward the outbound diversity target
24 | high | eclipse | Restart loses all peer state → guaranteed re-bootstrap dependence | Forced restart (e.g. via crash bug) resets victim into attacker's chosen seed | Warm-start from persisted peerstore + anchors
25 | high | eclipse | Peer key is `RemoteAddr().String()` including ephemeral source port for inbound (`p2p.go:90`) | One attacker host appears as N distinct "peers" via many source ports, defeating any later per-peer limits | Key/limit by remote IP (and /16 group), not IP:port
26 | high | eclipse | No outbound slot reservation that ignores inbound | Attacker saturates the box with inbound so node "feels connected" and stops dialing | Always maintain N outbound regardless of inbound count
27 | high | eclipse | No address-source grouping (Bitcoin keys new buckets by source group to limit poisoning) | Single source can populate the whole address table | Key new-table buckets by `hash(sourceGroup, addrGroup)`
28 | high | eclipse | No "test before trust": addresses are connected to directly, never feeler-tested | Attacker-fed dead/honeypot addrs consume real slots | Use feeler connections to vet addrs into the tried table before relying on them
29 | med | eclipse | Deterministic, source-ordered dialing (seeds dialed in slice order, `p2p.go:60-62`) | Predictable target order helps an attacker position itself first | Randomize selection order across buckets
30 | critical | sybil | No cost/PoW on advertising an address (no PEX today, but the design has no cost model) | Attacker injects unlimited fake addresses for free | Bound table size + bucket eviction so cheap injection can't dominate
31 | critical | sybil | Address-table poisoning is trivial because there is no bucketing/quota | Flood of attacker addrs evicts/crowds out honest addrs | new/tried buckets with bounded slots and group-keyed placement
32 | high | sybil | No limit on distinct addresses accepted per peer/connection | One peer floods the would-be table | Cap addrs accepted per peer per time window
33 | high | sybil | No reputation/scoring to resist Sybil identities | All identities equally trusted; many cheap nodes outvote few honest ones | Score peers (uptime, valid blocks, latency) and prefer high scorers
34 | high | sybil | No proof that an advertised address is reachable before it's trusted | Attacker advertises honest-looking but dead/own addresses | Feeler-test before promoting to tried
35 | med | sybil | No penalty for peers that supply invalid/unreachable addrs | Sybil can spam junk addrs with no consequence | Demote/ban peers whose advertised addrs repeatedly fail
36 | critical | peerstore | No persistence of known peers (no peers.json/db) | Every boot is a cold start that depends entirely on seeds | Persist a peerstore (addresses, buckets, lastSeen, scores) to disk
37 | critical | peerstore | Cold-start dependence on seeds every boot | Seed outage at restart time = node can't rejoin | Reconnect persisted/anchor peers first, seeds only as fallback
38 | high | peerstore | No peer quality scoring stored | Cannot prefer historically good peers after restart | Persist per-peer score and prefer high scorers on reconnect
39 | high | peerstore | No last-seen tracking | Cannot age out dead peers or prioritize fresh ones | Record and persist `lastSeen`; evict stale entries
40 | high | peerstore | No ban-list persistence (no ban list at all) | Banned/misbehaving peer reconnects freely after victim restart | Persist a banlist with expiry
41 | med | peerstore | No on-disk corruption/version handling for peerstore (because none exists) | Future peerstore format change bricks discovery | Version the peerstore file and tolerate parse errors gracefully
42 | med | peerstore | No bound on in-memory `n.peers` map (`p2p.go:38`) | Unbounded inbound connections grow the map without limit (memory DoS) | Cap total peers; reject when at capacity
43 | critical | NAT | No UPnP / NAT-PMP / PCP port mapping | Nodes behind NAT are inbound-unreachable; network drifts toward few reachable hubs (centralization) | Add UPnP/NAT-PMP to open the listen port automatically
44 | high | NAT | No external/public address discovery | Node cannot learn or advertise its reachable address | Discover external IP (STUN-like / peer-reported) and advertise it
45 | high | NAT | No advertised address in handshake (handshake carries only height, `p2p.go:102`) | Peers can't tell others where to reach this node | Include advertised `addr`+services in the version handshake
46 | high | NAT | No hole punching / relay for NAT'd peers | Two NAT'd nodes can never connect; reachable set shrinks | Add coordinated hole-punching or fallback relays
47 | med | NAT | Listen binds `0.0.0.0:18080` only — no explicit IPv6 (`::`) handling (`main.go:31`) | IPv6-only environments may not be reachable/dialable; mixed-stack discovery gaps | Bind dual-stack and handle v4/v6 address groups separately
48 | med | NAT | Port is effectively fixed at 18080 default and never auto-advertised | Peers assume default port; non-default operators are undiscoverable via addr-without-port assumptions | Always carry explicit port in addr records; allow configurable advertised port
49 | med | NAT | No reachability self-test ("am I inbound-reachable?") | Node may believe it's a full participant while accepting zero inbound | Probe own reachability via a peer and adjust advertising
50 | high | NAT | No distinction of routable vs non-routable (RFC1918/loopback) addresses in (future) addr handling | Private/loopback addrs would be gossiped network-wide, wasting slots and leaking topology | Filter non-routable addresses from relay/storage
51 | critical | connmgmt | Fixed 5s reconnect with no exponential backoff or jitter (`p2p.go:81,85`) | Many nodes losing a shared seed reconnect in lockstep → thundering herd hammering the seed | Exponential backoff with random jitter per address
52 | critical | connmgmt | One goroutine per seed loops forever even when the seed is permanently dead (`p2p.go:60,78-87`) | Goroutine + dial leak accumulates over time; permanently-dead seeds never abandoned | Bounded retries with backoff; abandon hopeless addresses
53 | high | connmgmt | No max-peers cap | Unbounded inbound accepts (`acceptLoop`, `p2p.go:67-75`) exhaust FDs/memory | Enforce max inbound + max outbound caps
54 | high | connmgmt | No min-peers target / no slot-filling logic | Node can sit at 1 peer indefinitely with no attempt to diversify | Drive toward a target outbound count continuously
55 | high | connmgmt | No feeler connections to test fresh addresses | Cannot validate "new" addrs before committing slots | Periodic short feeler connections to promote addrs to tried
56 | high | connmgmt | No eviction policy for connected peers | Cannot make room for better/diverse peers once slots are full | Implement eviction (protect best, evict by group/score)
57 | high | connmgmt | No churn handling: dropped outbound just sleeps 5s and re-dials same addr (`p2p.go:84-85`) | A flapping seed monopolizes a connection slot via constant reconnect | On drop, return slot to manager and pick a new candidate
58 | med | connmgmt | No connection timeout on the handshake/read loop (`handle` blocks on `readMsg`, `p2p.go:105-114`) | A silent peer holds a slot forever (slowloris) | Add idle/read deadlines and ping/pong liveness
59 | med | connmgmt | No ping/pong keepalive | Dead-but-open TCP connections look alive and waste slots | Add periodic ping with timeout-based disconnect
60 | med | connmgmt | `syncLoop` writes to all peers every 8s with no per-peer error handling beyond ignoring (`p2p.go:165-178`) | Writes to a wedged peer can block; no detection of stuck conns | Use write deadlines and drop unresponsive peers
61 | med | connmgmt | Self-connection not prevented (no handshake nonce) | A node can dial its own advertised address and waste a slot on itself | Add a random per-node nonce in version; drop self-connections
62 | med | connmgmt | Duplicate connections to the same peer not deduped (map keyed by IP:port allows multiple via inbound+outbound) | Two slots wasted on one peer; double gossip | Dedup by peer identity/IP and drop redundant links
63 | critical | privacy | Node IP is directly exposed to every connected peer (inherent to raw TCP, no overlay) | Trivial network-level deanonymization / geolocation of node operators | Offer Tor (and optionally I2P) transport like Monero
64 | critical | privacy | No Tor/I2P support at all (Monero ships Tor/I2P) | Operators cannot run privacy-preserving nodes; IP is always exposed | Add SOCKS5/Tor dialer + onion address advertising
65 | critical | privacy | No Dandelion++ — `msgTx` is immediately flooded to all peers (`p2p.go:160`) | First-relay deanonymization: an adversarial peer observing the origin learns the tx source IP, defeating the privacy coin's purpose | Implement Dandelion++ stem/fluff phases for tx relay
66 | high | privacy | Address timestamp leak (when PEX added) reveals when a node was last seen/online | Operator activity patterns and uptime are profiled | Fuzz/round timestamps and limit precision in addr relay
67 | high | privacy | Linking node IP to advertised addresses ties wallet/RPC activity to a network identity | Correlating tx origin with the relaying IP deanonymizes users | Separate relay identity from origin; Tor + Dandelion++
68 | high | privacy | Block/tx relay reveals origin via timing of first announcement | Spy nodes triangulate the source by earliest-receive timing | Add relay delays/Dandelion++ stem to break timing correlation
69 | med | privacy | No outbound-only / "blocks-only" mode to limit topology exposure | Inbound peers map the node's connection graph | Offer outbound-only and Tor-only modes
70 | med | privacy | Single network identity per node (no per-connection address rotation over Tor) | Long-term linkability of a node across observations | Support stream isolation over Tor
71 | critical | protocol | No network magic/id on the wire (`NetworkSeed` is unused in p2p) — handshake is just `msgHello`(height) | Testnet/mainnet/fork nodes cross-connect and corrupt each other's view; foreign protocols can connect | Prefix every message with a 4-byte network magic; reject mismatches
72 | critical | protocol | No protocol version gate in handshake (`p2p.go:101-103`) | Incompatible versions connect and exchange malformed data; no upgrade negotiation | Add a `version` message with protocol version and min-accepted version
73 | high | protocol | No service bits/flags (full node, pruned, blocks-only, witness, etc.) | Cannot select peers by capability; wastes slots on incompatible peers | Add a services bitfield to the handshake and select on it
74 | high | protocol | No user-agent string | Cannot diagnose, segment, or ban buggy/malicious client versions | Add a length-bounded user-agent field
75 | high | protocol | `msgHello` payload (height) is parsed for nothing (`p2p.go:119-120`) | The only handshake field is effectively ignored; no validation occurs | Replace with a real version handshake whose fields gate the connection
76 | high | protocol | No handshake completion gate before processing data messages | A peer can send `msgBlock`/`msgTx` before any handshake (dispatch runs immediately, `p2p.go:113`) | Require a completed version/verack exchange before accepting other messages
77 | med | protocol | No genesis/chain-id check in handshake | Two coins sharing this code/port silently interconnect | Include genesis hash in version and reject mismatches
78 | med | protocol | No timestamp exchange / clock-offset detection in handshake | No protection against time-skew peers (affects future time-based addr aging) | Exchange peer time and warn/penalize on large skew
79 | critical | dos | Address-table memory exhaustion path is wide open once PEX exists (no caps designed) | Attacker floods addrs to OOM the node | Hard-cap table size with bucket eviction
80 | high | dos | Getaddr amplification (when added): no rate limit means repeated `getaddr` yields large `addr` replies | Reflection/amplification DoS against third parties | Answer `getaddr` at most once per connection per interval, cached
81 | high | dos | Connection-slot exhaustion: unbounded `acceptLoop` (`p2p.go:67-75`) | Attacker opens thousands of inbound conns, starving honest peers and FDs | Cap inbound, per-IP inbound limits, evict on pressure
82 | high | dos | No rate limiting on any discovery/control message | Attacker spams `msgGetTip`/`msgGetBlk` forcing disk/CPU work (`p2p.go:121-133`) | Per-peer message rate limiting and misbehavior scoring
83 | high | dos | `msgGetBlk` lets any unauthenticated peer pull arbitrary blocks at full rate (`p2p.go:129-133`) | Bandwidth-amplification / scraping DoS | Rate-limit block serving; require completed handshake
84 | high | dos | 64 MiB max message (`p2p.go:228`) × many connections = large memory amplification | Many peers each sending near-max messages exhaust memory | Lower per-message cap by type; bound concurrent in-flight buffers
85 | med | dos | `readMsg` allocates `make([]byte, n)` up front for declared length (`p2p.go:231`) | Attacker declares 64 MiB but trickles bytes (slowloris + memory hold) | Stream/limit allocation; enforce read deadlines
86 | med | dos | No per-IP connection rate limiting on accept | Rapid connect/disconnect churn (connection storm) DoS | Rate-limit new inbound per source IP
87 | med | dos | No misbehavior scoring → no automatic disconnect/ban of abusive peers | Abusers persist indefinitely | Add a ban-score that disconnects/bans on threshold
88 | critical | determinism | Predictable peer selection: seeds dialed in fixed slice order (`main.go:57`, `p2p.go:60-62`) | Attacker predicts/positions which peers a node connects to first | Randomize candidate selection across buckets
89 | high | determinism | No randomized eviction (no eviction exists) | When eviction is added naively, deterministic choice is gameable | Use randomized, group-aware eviction with protected slots
90 | high | determinism | Time-based seed: miner wallet seed uses `time.Now().UnixNano()` (`main.go:157`) — discovery-adjacent identity is low-entropy/time-derived | Predictable/guessable node identity material if reused for handshake nonces later | Use crypto/rand for any node identity/nonce, never time
91 | high | determinism | No randomized address-bucket placement (no buckets) | Future bucketing without randomization is poisonable | Key bucket index with a per-node random secret (Bitcoin does this)
92 | med | determinism | `syncLoop` ticker is a fixed 8s with no jitter (`p2p.go:166`) | Synchronized `getTip` bursts across the network (mini thundering herd) | Add jitter to periodic discovery/sync timers
93 | high | eclipse | No outbound connection to diverse network groups guaranteed (no group logic) | All 8 outbounds could land in one ASN | Require outbounds span distinct ASNs/network groups
94 | high | peerstore | No tracking of which addresses were ever successfully connected | Cannot build a "tried" set; every restart re-vets everything | Mark addrs connected-at-least-once and persist that flag
95 | med | bootstrap | No staggering of seed dials (all `go n.connect` fire at once, `p2p.go:60-62`) | Simultaneous connect burst against shared seeds | Stagger initial dials with small random delays
96 | med | PEX | No "tried" address re-advertisement policy (none exists) | Even with PEX, only self-addr or all-addr extremes are possible | Relay a bounded random sample of fresh tried addresses
97 | med | connmgmt | `handle()` has no per-peer send queue; `Broadcast` writes synchronously under the lock (`p2p.go:182-191`) | One slow peer blocks broadcast to all peers, stalling propagation (slot/throughput DoS) | Per-peer async send queues with drop-on-overflow
98 | high | sybil | No inbound-peer eviction protections, so honest inbound peers are indistinguishable from Sybil inbound | Sybil inbound flood looks identical to honest growth | Apply group limits + scoring to inbound acceptance
99 | high | eclipse | Re-eclipse on restart is deterministic because the same `--seeds` is reused with no learned diversity | Operator who pins one seed flag is permanently eclipsable | Persist peerstore/anchors so restarts don't reset to the seed-only view
100 | med | protocol | No graceful protocol-version downgrade/upgrade signaling | Network-wide upgrades risk silent splits as old/new nodes can't negotiate | Use min/max protocol version in `version` to allow staged upgrades

---

## Recommended Auto-Discovery Design

A concrete, implementable plan to bring Obscura's discovery up to Bitcoin/Monero-class robustness. Designed to layer onto the existing `pkg/p2p` framing without breaking the current message numbering (extend, don't renumber).

### 1. Wire protocol: magic + version handshake

Add a 4-byte **network magic** in front of every framed message (or as a connection preamble). Derive distinct magics per network:

```
mainnet magic = first 4 bytes of SHA256("obscura-mainnet-v1")   // reuse config.NetworkSeed
testnet magic = first 4 bytes of SHA256("obscura-testnet-v1")
```

Reject any connection whose first frame's magic mismatches. This fixes findings #71, #77.

New handshake messages (replace the `msgHello`/`msgGetTip` opener):

- `msgVersion` payload: `protoVersion(u32)`, `minProtoVersion(u32)`, `services(u64 bitfield)`, `timestamp(i64)`, `nonce(u64, crypto/rand)`, `genesisHash(32B)`, `advertisedAddr(ip:port)`, `userAgent(len-prefixed, ≤64B)`, `startHeight(u64)`.
- `msgVerack` to confirm.

Rules: drop self-connections by remembering our own `nonce` (#61); reject on magic/genesis/version mismatch (#71–#73, #76); **do not dispatch any other message type until verack is exchanged** (#76). Services bits: `NODE_FULL=1`, `NODE_PRUNED=2`, `NODE_BLOCKSONLY=4`, `NODE_TOR=8`.

### 2. Address types: PEX

Add `msgGetAddr` (no payload) and `msgAddr` (payload = count-prefixed list of `{time(i64), services(u64), ip(16B), port(u16)}`, max 1000 entries).

- After verack, send one `getaddr` to outbound peers.
- Answer `getaddr` **at most once per connection**, from a cached random sample of up to 1000 fresh tried addresses (#80, #96).
- On receiving `addr`: filter non-routable (RFC1918/loopback/multicast) (#50), cap entries (#17), rate-limit per peer (#15, #82), insert into the **new** table.
- Relay rule: if `addr` has ≤10 entries, forward to 1–2 deterministically-chosen peers (Bitcoin-style) with fuzzed timestamps (#66); never flood large `addr` (#15).
- Self-advertisement: every ~24h relay our own `advertisedAddr` with current timestamp to 2 peers (#14).

### 3. Address manager: new/tried buckets (Bitcoin AddrMan model)

Two tables:

- **new**: addresses learned but never successfully connected. 1024 buckets × 64 slots. Bucket index = `H(secretKey, sourceGroup, addrGroup) % 1024` where group = /16 for IPv4, /32 for IPv6. Keying by **source group** means one attacker source can only touch a few buckets → poisoning-resistant (#21, #27, #30, #31, #91).
- **tried**: addresses we have connected to successfully. 256 buckets × 64 slots, indexed by `H(secretKey, addrGroup)`.

`secretKey` is a per-node 32-byte `crypto/rand` value persisted in the peerstore (#90, #91). Eviction is **randomized + position-collision based** (terrible/oldest entry evicted on collision) (#89). Promotion new→tried happens only after a successful feeler/real connection (#28, #34, #55, #94).

### 4. Persistent peerstore (`peers.json`)

Persist to `<datadir>/peers.json` (versioned, corruption-tolerant — #41):

```json
{
  "version": 1,
  "key": "<hex 32B addrman secret>",
  "anchors": ["ip:port", "ip:port"],
  "new":   [{"addr":"ip:port","time":..., "src":"ip", "services":1}],
  "tried": [{"addr":"ip:port","time":..., "attempts":0, "lastSuccess":..., "score":...}],
  "banned": [{"ip":"...", "until": <unix>}]
}
```

Save periodically and on shutdown. On boot: load peerstore, reconnect **anchors first** (#22, #24, #37, #99), then fill from tried, then new, then seeds/DNS only if short. Track `lastSeen`, `attempts`, success/score (#38, #39, #94) and persisted bans (#40).

### 5. Bootstrap pipeline (layered, with fallback)

Order of address sources (#6):

1. `--seeds` flag (manual override).
2. Persisted peerstore (anchors + tried).
3. **DNS seeders**: ship `dnsSeeds = ["seed1.obscura.example", ...]`; resolve A/AAAA at startup and when the new+tried tables run low (#3).
4. **Hardcoded fallback seeds**: embed ≥6 IP:port literals across independent operators/ASNs in `pkg/config` (#2, #4).

Seeds are treated as **untrusted addr sources only**: connect, `getaddr`, harvest addrs, then disconnect once new+tried are populated (#8, #10). If a seed keeps failing, abandon it and move to the next layer (#5). Stagger initial dials with small random delays (#95).

### 6. Connection manager

Replace the per-seed infinite loop (`p2p.go:77-87`) with a central manager:

- Targets: **8 outbound** (target), **125 inbound** cap (#53, #81). Separate `outbound` and `inbound` peer sets (#23, #26, #98).
- **Diversity**: at most 1–2 outbound per /16 IPv4 group (#20, #25); require outbounds to span ≥4 distinct ASNs/network groups where possible (#93). Limit inbound per source IP (#86).
- **Feeler**: every ~2 minutes, open one short connection to a random **new**-table address, handshake, then disconnect — promoting it to tried on success (#28, #55).
- **Anchors**: on clean shutdown, persist the 2 longest-lived good outbound peers as anchors (#22).
- **Backoff**: per-address exponential backoff with jitter — `delay = min(maxDelay, base * 2^attempts) ± rand` (#51, #52); abandon an address after N failures. Kill the goroutine-per-dead-seed leak (#52).
- **Eviction**: when inbound is full, evict using Bitcoin-style protection (protect by netgroup diversity, longest-uptime, last-block-time; evict youngest in the largest netgroup) (#56).
- **Liveness**: handshake/read deadlines (#58), `ping`/`pong` keepalive (#59), write deadlines (#60), per-peer async send queue so one slow peer can't stall `Broadcast` (#97).
- **Self/dup**: drop self (nonce) and duplicate-peer connections (#61, #62).
- Add jitter to `syncLoop`/periodic timers (#92).

### 7. NAT / reachability

- **UPnP + NAT-PMP/PCP** to map the listen port on startup (#43).
- **External address discovery**: learn own public IP from peer-reported `addr` (peers echo what address they saw us connect from) and/or a STUN-like probe; advertise it in `msgVersion` (#44, #45, #49).
- **Dual-stack**: bind `[::]` and `0.0.0.0`, treat v4/v6 as separate netgroups (#47). Always carry explicit port in addr records (#48).

### 8. DoS hardening

- Per-message-type size caps (blocks vs control); lower the blanket 64 MiB and bound concurrent in-flight allocations; stream-read instead of pre-allocating declared length (#84, #85).
- Per-peer message rate limits on all control/discovery messages; rate-limit `getaddr`, `getblk`, block serving (#80, #82, #83).
- **Misbehavior ban-score**: increment on invalid messages, bogus addrs, oversized payloads, pre-handshake data; disconnect + persist ban at threshold (#35, #87).
- Cap total addr-table memory; bucket eviction prevents exhaustion (#79).

### 9. Privacy

- **Tor/I2P**: optional SOCKS5 dialer (`--proxy`, `--onion`), advertise an `.onion`/I2P address with `NODE_TOR` service bit; support `-onlynet=tor`; stream isolation for separate connections (#63, #64, #70). Provide outbound-only / blocks-only modes (#69).
- **Dandelion++** for tx relay: replace immediate `msgTx` flood (`p2p.go:160`) with a stem phase (relay to a single pseudorandom successor for a random hop count / epoch) then fluff (normal broadcast). Per-epoch random successor selection; embargo timer to force fluff if stem stalls (#65, #67, #68).
- Fuzz/round addr timestamps; never relay non-routable or origin-identifying addrs (#66).

### 10. Determinism / security hygiene

- All randomness (nonces, bucket secret, jitter, selection, eviction) from `crypto/rand`; never `time.Now()` for security-relevant values (#88, #90, #91).
- Randomized candidate selection and randomized eviction throughout (#29, #88, #89).

### Suggested file layout (new, not yet present)

- `pkg/p2p/addrman.go` — new/tried buckets, group keying, eviction.
- `pkg/p2p/peerstore.go` — `peers.json` load/save, anchors, banlist.
- `pkg/p2p/connman.go` — outbound/inbound targets, feelers, backoff, diversity, eviction.
- `pkg/p2p/handshake.go` — magic, version/verack, services, nonce.
- `pkg/p2p/dnsseed.go` + `config` additions — DNS + hardcoded seeds, network magic.
- `pkg/p2p/dandelion.go` — stem/fluff tx relay.
- `pkg/p2p/tor.go` — SOCKS5/onion transport.

---

*End of audit. This document is the only file written by this review; no `.go`, `pkg/`, or `cmd/` files were modified.*
