package p2p

import (
	"net"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// Dialer abstracts how the node makes OUTBOUND connections, so the same gossip
// logic runs over clearnet TCP or over Tor (a local SOCKS5 proxy). This gives
// optional network-layer anonymity (hiding the node's IP) on top of the
// Dandelion++ transaction-origin privacy.
type Dialer interface {
	Dial(addr string) (net.Conn, error)
}

// clearnetDialer is the default: plain TCP with a timeout.
type clearnetDialer struct{ timeout time.Duration }

func (d clearnetDialer) Dial(addr string) (net.Conn, error) {
	return net.DialTimeout("tcp", addr, d.timeout)
}

// torDialer routes every outbound connection through a local Tor SOCKS5 proxy
// (so peers and observers never see the node's real IP, and .onion peers are
// reachable).
type torDialer struct{ d proxy.Dialer }

func (t torDialer) Dial(addr string) (net.Conn, error) {
	return t.d.Dial("tcp", addr)
}

// NewTorDialer builds a Dialer that connects through the Tor SOCKS5 proxy at
// socksAddr (typically 127.0.0.1:9050). Hostname resolution is delegated to Tor
// (proxy.SOCKS5 sends the host to the proxy), so there is no local DNS leak.
func NewTorDialer(socksAddr string) (Dialer, error) {
	d, err := proxy.SOCKS5("tcp", socksAddr, nil, &net.Dialer{Timeout: 20 * time.Second})
	if err != nil {
		return nil, err
	}
	return torDialer{d: d}, nil
}

// SetTransport configures the outbound dialer and the address this node
// advertises to peers (for Tor, the node's .onion address; for clearnet, its
// reachable host:port). Call before Start. Passing dialer=nil keeps clearnet.
// When onionOnly is true (Tor mode), the node FAILS CLOSED on the address layer:
// it only stores/relays .onion peer addresses, never mixing clearnet peers into
// the anonymity set (per the engine's pitfall #2).
func (n *Node) SetTransport(dialer Dialer, advertiseAddr string, onionOnly bool) {
	if dialer != nil {
		n.dialer = dialer
	}
	n.advMu.Lock()
	n.advertiseAddr = advertiseAddr
	n.advertiseFixed = true // a Tor .onion address is fixed — never auto-overridden
	n.advMu.Unlock()
	n.onionOnly = onionOnly
}

// isOnion reports whether addr is a Tor hidden-service address.
func isOnion(addr string) bool {
	h := addr
	if host, _, err := net.SplitHostPort(addr); err == nil {
		h = host
	}
	return strings.HasSuffix(h, ".onion")
}

// maybeAddAddr records a learned peer address, dropping clearnet addresses when
// the node is in onion-only (Tor) mode.
func (n *Node) maybeAddAddr(addr string) {
	if n.onionOnly && !isOnion(addr) {
		return // fail closed: never mix clearnet peers into the Tor anonymity set
	}
	if !n.onionOnly && !isRoutable(addr) {
		return // never store/share an undialable address (0.0.0.0 etc.) via PEX
	}
	// Never store OUR OWN public address: it echoes back via PEX, and keeping it in the
	// book leads to self-dials (slots/sync wasted on a self-loop instead of real peers).
	n.advMu.RLock()
	self := n.advertiseAddr
	n.advMu.RUnlock()
	if addr == self || addr == n.addr {
		return
	}
	n.book.Add(addr)
}

// KnownAddresses returns the addresses currently in the peer address book
// (primarily for tests / introspection).
func (n *Node) KnownAddresses() []string { return n.book.Sample(1 << 16) }
