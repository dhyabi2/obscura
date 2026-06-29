// Package tor tests the optional Tor transport (Block 10): a pluggable outbound
// dialer and .onion address advertisement, without requiring a real Tor daemon.
package tor

import (
	"net"
	"sync/atomic"
	"testing"
	"time"

	"obscura/pkg/mempool"
	"obscura/pkg/p2p"
	"obscura/tests/critical/harness"
)

func poll(d time.Duration, f func() bool) bool {
	dl := time.Now().Add(d)
	for time.Now().Before(dl) {
		if f() {
			return true
		}
		time.Sleep(150 * time.Millisecond)
	}
	return f()
}

// recordingDialer is a Dialer that proves the node uses the injected transport
// for ALL outbound connections (the hook a real Tor SOCKS5 dialer plugs into).
type recordingDialer struct{ n int32 }

func (d *recordingDialer) Dial(addr string) (net.Conn, error) {
	atomic.AddInt32(&d.n, 1)
	return net.DialTimeout("tcp", addr, 5*time.Second)
}

// TestInjectedDialerUsed: outbound connections go through the configured dialer,
// and the node still syncs — i.e. the transport is genuinely pluggable (Tor
// swaps in here).
func TestInjectedDialerUsed(t *testing.T) {
	defer harness.SmallMaturity()()
	chA := harness.NewChain(t)
	chB := harness.NewChain(t)
	harness.MineN(t, chA, harness.NewWallet("tor-miner"), 4)

	nodeA := p2p.NewNode("127.0.0.1:19701", chA, mempool.New(chA), "")
	nodeB := p2p.NewNode("127.0.0.1:19702", chB, mempool.New(chB), "")
	rec := &recordingDialer{}
	nodeB.SetTransport(rec, "", false) // B dials via the injected transport
	defer nodeA.Stop()
	defer nodeB.Stop()
	if err := nodeA.Start(nil); err != nil {
		t.Fatal(err)
	}
	if err := nodeB.Start([]string{"127.0.0.1:19701"}); err != nil {
		t.Fatal(err)
	}

	if !poll(30*time.Second, func() bool { return chB.Height() == chA.Height() }) {
		t.Fatalf("sync via injected dialer failed: A=%d B=%d", chA.Height(), chB.Height())
	}
	if atomic.LoadInt32(&rec.n) == 0 {
		t.Fatal("injected dialer was never used for outbound connections")
	}
}

// TestOnionAddressAdvertised: a node advertising a .onion address propagates it
// to peers (so the peer id on the wire is the onion, not an IP).
func TestOnionAddressAdvertised(t *testing.T) {
	defer harness.SmallMaturity()()
	chA := harness.NewChain(t)
	chB := harness.NewChain(t)
	harness.MineN(t, chA, harness.NewWallet("tor-miner2"), 2)

	onion := "obscuratestonionaddr234567.onion:18080"
	nodeA := p2p.NewNode("127.0.0.1:19711", chA, mempool.New(chA), "")
	nodeB := p2p.NewNode("127.0.0.1:19712", chB, mempool.New(chB), "")
	// B advertises an onion id but uses a clearnet dialer so it can reach A here.
	nodeB.SetTransport(nil, onion, false)
	defer nodeA.Stop()
	defer nodeB.Stop()
	if err := nodeA.Start(nil); err != nil {
		t.Fatal(err)
	}
	if err := nodeB.Start([]string{"127.0.0.1:19711"}); err != nil {
		t.Fatal(err)
	}

	// A should learn B's advertised .onion address from the handshake.
	ok := poll(20*time.Second, func() bool {
		for _, a := range nodeA.KnownAddresses() {
			if a == onion {
				return true
			}
		}
		return false
	})
	if !ok {
		t.Fatalf("peer did not learn the advertised onion address; known=%v", nodeA.KnownAddresses())
	}
}
