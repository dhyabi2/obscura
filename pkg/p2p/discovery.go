package p2p

import (
	"log"
	"net"
)

// Self-address discovery — the mechanism that makes the network SEED-INDEPENDENT and
// censorship-resistant. A node learns its own PUBLIC, dialable address from its peers (the
// Bitcoin "addr-me" approach: a peer reports the source address it saw us connect from), so
// PEX propagates REACHABLE addresses with no hardcoded or manually-configured seed list.
//
// Why it matters: if nodes advertise an undialable address (e.g. the 0.0.0.0 listen addr),
// peers only ever learn the bootstrap nodes' addresses (a STAR topology), and killing the
// bootstrap nodes FRAGMENTS the network. With self-discovery every peer learns every other
// peer's real address, so the bootstrap nodes are pure convenience — once any node has a
// few peers, it keeps the full mesh via the persisted address book + PEX, and no party can
// censor or stop the network by taking down the original seeds.

// isRoutable rejects addresses that must never be advertised or shared via PEX: the
// unspecified address (0.0.0.0 / ::) and empty/zero-port garbage. Loopback and private
// ranges are PERMITTED so local/LAN devnets work; on a public network a peer simply fails
// to dial them and the address book evicts them via its failure counter.
func isRoutable(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" || port == "0" || host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		return false
	}
	return true
}

// getAdvertise returns the address announced to peers (guarded; updated by self-discovery).
func (n *Node) getAdvertise() string {
	n.advMu.RLock()
	defer n.advMu.RUnlock()
	return n.advertiseAddr
}

// SetAdvertise pins the node's advertised address explicitly (operator override — e.g. a
// droplet's public IP, or a static DNS name). Disables peer-observed auto-learning so the
// operator stays in control.
func (n *Node) SetAdvertise(addr string) {
	if addr == "" {
		return
	}
	n.advMu.Lock()
	n.advertiseAddr = addr
	n.advertiseFixed = true
	n.advMu.Unlock()
}

// minSelfDiscoveryGroups is how many DISTINCT /16 reporter networks must independently
// agree on our observed public IP before we adopt it. Counting raw votes (the old way) let
// a single attacker we made ≥2 outbound connections to report a bogus IP twice and poison
// our advertised address (audit: 2-vote self-discovery poisoning). Requiring agreement
// across separate networks makes that materially harder.
//
// audit fix (self-discovery Sybil): a threshold of 2 was still a low bar — a Sybil
// controlling just two /16 networks that the victim outbound-dials could forge a false
// advertised IP. Require FOUR distinct reporter networks. An honest node behind NAT sees
// far more than four distinct-network peers observe the same IP, so auto-discovery still
// converges; a Sybil now needs four separate /16s the victim independently dials, which the
// per-/16 inbound cap and reserved-outbound slots (admitInbound/discoveryLoop) make hard to
// arrange. Operators who want certainty pin the address with --advertise (SetAdvertise).
const minSelfDiscoveryGroups = 4

// learnExternalFromPeer records a peer's observation of OUR source address (the address the
// peer saw when we dialed it), attributed to the REPORTING peer (reporterAddr). We adopt
// <observed IP>:<our listen port> only when ≥minSelfDiscoveryGroups DISTINCT reporter /16
// networks independently agree on the same routable public IP — fully decentralized, zero
// config, and resistant to a single network poisoning it. An explicit SetAdvertise wins.
func (n *Node) learnExternalFromPeer(observed, reporterAddr string) {
	host := hostOf(observed)
	ip := net.ParseIP(host)
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() {
		return // can't learn a useful public address from a loopback/garbage observation
	}
	_, myPort, err := net.SplitHostPort(n.addr)
	if err != nil {
		return
	}
	cand := net.JoinHostPort(host, myPort)
	if !isRoutable(cand) {
		return
	}
	group := ipGroup(reporterAddr) // /16 of the peer that reported this observation
	n.advMu.Lock()
	if n.advertiseFixed {
		n.advMu.Unlock()
		return
	}
	if n.extVotes == nil {
		n.extVotes = make(map[string]map[string]bool)
	}
	if n.extVotes[cand] == nil {
		n.extVotes[cand] = make(map[string]bool)
	}
	n.extVotes[cand][group] = true
	adopt := len(n.extVotes[cand]) >= minSelfDiscoveryGroups && cand != n.advertiseAddr
	if adopt {
		n.advertiseAddr = cand
	}
	n.advMu.Unlock()
	if adopt {
		log.Printf("p2p: discovered public address %s (agreed by %d distinct networks) — now advertised", cand, minSelfDiscoveryGroups)
	}
}
