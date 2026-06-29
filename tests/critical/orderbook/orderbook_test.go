// Package orderbook tests the decentralized swap order book (Block 15): signed,
// PoW-stamped, expiring offers; the book; and P2P offer propagation.
package orderbook

import (
	"net/http/httptest"
	"testing"
	"time"

	"obscura/pkg/commit"
	"obscura/pkg/mempool"
	"obscura/pkg/p2p"
	"obscura/pkg/rpc"
	"obscura/pkg/swapbook"
	"obscura/tests/critical/harness"
)

func makeOffer(t *testing.T, give, get string, giveAmt, getAmt uint64) *swapbook.Offer {
	t.Helper()
	sec := commit.RandomScalar()
	o := &swapbook.Offer{
		GiveAsset: give, GetAsset: get,
		GiveAmount: giveAmt, GetAmount: getAmt,
		Expiry: time.Now().Add(time.Hour).Unix(),
	}
	o.Sign(sec)
	return o
}

func TestOfferSignAndVerify(t *testing.T) {
	o := makeOffer(t, "OBX", "XNO", 100, 1)
	if !o.Verify(time.Now()) {
		t.Fatal("valid offer rejected")
	}
	// tamper the amount → signature/PoW no longer matches
	o.GiveAmount = 999
	if o.Verify(time.Now()) {
		t.Fatal("tampered offer accepted")
	}
}

func TestOfferExpiry(t *testing.T) {
	sec := commit.RandomScalar()
	o := &swapbook.Offer{GiveAsset: "OBX", GetAsset: "XNO", GiveAmount: 10, GetAmount: 1,
		Expiry: time.Now().Add(-time.Minute).Unix()}
	o.Sign(sec)
	if o.Verify(time.Now()) {
		t.Fatal("expired offer accepted")
	}
}

func TestOfferPoW(t *testing.T) {
	o := makeOffer(t, "OBX", "XNO", 100, 1)
	id := o.ID()
	// the signed offer must satisfy the PoW (leading zero bits)
	zeros := 0
	for _, b := range id {
		if b == 0 {
			zeros += 8
			continue
		}
		for i := 7; i >= 0 && b&(1<<uint(i)) == 0; i-- {
			zeros++
		}
		break
	}
	if zeros < swapbook.OfferPoWBits {
		t.Fatalf("offer PoW too weak: %d < %d", zeros, swapbook.OfferPoWBits)
	}
}

func TestBookDedupAndBest(t *testing.T) {
	b := swapbook.NewBook()
	o1 := makeOffer(t, "OBX", "XNO", 100, 1) // 100 OBX per 1 XNO
	o2 := makeOffer(t, "OBX", "XNO", 120, 1) // better for an XNO->OBX taker
	isNew, err := b.Add(o1)
	if err != nil || !isNew {
		t.Fatalf("add o1: new=%v err=%v", isNew, err)
	}
	again, _ := b.Add(o1)
	if again {
		t.Fatal("duplicate offer counted as new")
	}
	b.Add(o2)
	if b.Size() != 2 {
		t.Fatalf("book size = %d, want 2", b.Size())
	}
	// taker gives XMR, wants OBX → best is the offer giving the most OBX per XMR
	best := b.Best("XNO", "OBX")
	if best == nil || best.GiveAmount != 120 {
		t.Fatalf("Best picked the wrong offer: %+v", best)
	}
}

// TestOfferPropagation: an offer posted on one node reaches a connected peer's
// order book via gossip.
func TestOfferPropagation(t *testing.T) {
	defer harness.SmallMaturity()()
	cA := harness.NewChain(t)
	cB := harness.NewChain(t)
	nodeA := p2p.NewNode("127.0.0.1:19751", cA, mempool.New(cA), "")
	nodeB := p2p.NewNode("127.0.0.1:19752", cB, mempool.New(cB), "")
	defer nodeA.Stop()
	defer nodeB.Stop()
	if err := nodeA.Start(nil); err != nil {
		t.Fatal(err)
	}
	if err := nodeB.Start([]string{"127.0.0.1:19751"}); err != nil {
		t.Fatal(err)
	}

	o := makeOffer(t, "OBX", "XNO", 250, 1)
	// wait for the link, then post on A
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && nodeA.PeerCount() == 0 {
		time.Sleep(150 * time.Millisecond)
	}
	if err := nodeA.PostOffer(o); err != nil {
		t.Fatalf("post offer: %v", err)
	}
	ok := false
	dl2 := time.Now().Add(20 * time.Second)
	for time.Now().Before(dl2) {
		if len(nodeB.Offers()) >= 1 {
			ok = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !ok {
		t.Fatalf("offer did not propagate to peer; B has %d offers", len(nodeB.Offers()))
	}
}

// TestOrderBookRPC: a client posts an offer over RPC and lists it back.
func TestOrderBookRPC(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	node := p2p.NewNode("127.0.0.1:19761", c, mempool.New(c), "")
	defer node.Stop()
	// no Start needed: the node's order book works standalone for RPC.

	srv := rpc.NewServer(c, mempool.New(c), nil)
	srv.SetOfferBook(node)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	cl, err := rpc.NewClient(ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	o := makeOffer(t, "OBX", "XNO", 300, 1)
	if err := cl.PostOffer(o); err != nil {
		t.Fatalf("post offer via RPC: %v", err)
	}
	got, err := cl.Offers()
	if err != nil {
		t.Fatalf("list offers via RPC: %v", err)
	}
	if len(got) != 1 || got[0].GiveAmount != 300 {
		t.Fatalf("RPC order book wrong: %+v", got)
	}
}

// TestSeedDerivedOfferKey mirrors the wallet CLI `offer` command: the maker
// signing key is derived deterministically from the wallet seed, so the same
// seed always signs as the same maker and the offer verifies.
func TestSeedDerivedOfferKey(t *testing.T) {
	seed := []byte("obscura-cli-test-seed-0000000000")
	derive := func() *swapbook.Offer {
		sec := commit.HashToScalar([]byte("Obscura/offer-key"), seed)
		o := &swapbook.Offer{
			GiveAsset: "OBX", GetAsset: "XNO",
			GiveAmount: 500, GetAmount: 2,
			Expiry: time.Now().Add(time.Hour).Unix(),
		}
		o.Sign(sec)
		return o
	}
	o1, o2 := derive(), derive()
	if !o1.Verify(time.Now()) {
		t.Fatal("seed-derived offer failed to verify")
	}
	// same seed → same maker pubkey (deterministic identity)
	if string(o1.Maker) != string(o2.Maker) {
		t.Fatal("offer maker key is not deterministic across runs")
	}
	// a different seed → a different maker
	other := commit.HashToScalar([]byte("Obscura/offer-key"), []byte("different-seed-aaaaaaaaaaaaaaaaaa"))
	oo := &swapbook.Offer{GiveAsset: "OBX", GetAsset: "XNO", GiveAmount: 500, GetAmount: 2,
		Expiry: time.Now().Add(time.Hour).Unix()}
	oo.Sign(other)
	if string(oo.Maker) == string(o1.Maker) {
		t.Fatal("distinct seeds produced the same maker key")
	}
}
