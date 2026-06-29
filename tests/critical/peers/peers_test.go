// Package peers tests the /peers RPC (Block 41): it reports connected-peer count
// and addresses from the provider.
package peers

import (
	"net/http/httptest"
	"testing"

	"obscura/pkg/mempool"
	"obscura/pkg/rpc"
	"obscura/tests/critical/harness"
)

// mockPeers implements rpc.PeerProvider.
type mockPeers struct{ addrs []string }

func (m mockPeers) PeerCount() int      { return len(m.addrs) }
func (m mockPeers) PeerAddrs() []string { return m.addrs }

// PeerForMaker satisfies rpc.PeerProvider (added with offer→peer routing); the
// peers test doesn't exercise maker routing, so it reports "unknown".
func (m mockPeers) PeerForMaker(maker []byte) (string, bool) { return "", false }

func TestPeersRPC(t *testing.T) {
	c := harness.NewChain(t)
	srv := rpc.NewServer(c, mempool.New(c), nil)
	srv.SetPeerProvider(mockPeers{addrs: []string{"1.2.3.4:5000", "5.6.7.8:5000"}})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	cl, err := rpc.NewClient(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	p, err := cl.Peers()
	if err != nil {
		t.Fatalf("peers: %v", err)
	}
	if p.Count != 2 || len(p.Peers) != 2 {
		t.Fatalf("got count=%d peers=%v", p.Count, p.Peers)
	}
}

func TestPeersRPCNoProvider(t *testing.T) {
	c := harness.NewChain(t)
	srv := rpc.NewServer(c, mempool.New(c), nil) // no peer provider
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	cl, _ := rpc.NewClient(ts.URL)
	p, err := cl.Peers()
	if err != nil {
		t.Fatalf("peers: %v", err)
	}
	if p.Count != 0 || len(p.Peers) != 0 {
		t.Fatalf("expected empty peers, got %+v", p)
	}
}
