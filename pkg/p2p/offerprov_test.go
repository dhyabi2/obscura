package p2p_test

import (
	"encoding/hex"
	"testing"
	"time"

	"filippo.io/edwards25519"

	"obscura/pkg/chain"
	"obscura/pkg/commit"
	"obscura/pkg/mempool"
	"obscura/pkg/p2p"
	"obscura/pkg/swapbook"
)

// TestOfferProvenanceRecordsMakerPeer proves the maker-pubkey -> peer directory: a
// maker node posts a signed OBX/XNO offer; a taker node that receives the gossiped
// offer must be able to resolve the maker's peer via PeerForMaker (so /swaps/take can
// route the Init to the maker instead of guessing PeerAddrs()[0]). It also confirms
// an unknown maker resolves to (.., false).
func TestOfferProvenanceRecordsMakerPeer(t *testing.T) {
	chainA, err := chain.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer chainA.Close()
	chainB, err := chain.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer chainB.Close()

	addrA := "127.0.0.1:19601"
	addrB := "127.0.0.1:19602"
	nodeA := p2p.NewNode(addrA, chainA, mempool.New(chainA), "") // maker
	nodeB := p2p.NewNode(addrB, chainB, mempool.New(chainB), "") // taker
	defer nodeA.Stop()
	defer nodeB.Stop()
	if err := nodeA.Start(nil); err != nil {
		t.Fatalf("start A: %v", err)
	}
	if err := nodeB.Start([]string{addrA}); err != nil {
		t.Fatalf("start B: %v", err)
	}

	// wait for the connection.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if nodeA.PeerCount() > 0 && nodeB.PeerCount() > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if nodeA.PeerCount() == 0 || nodeB.PeerCount() == 0 {
		t.Fatal("nodes did not connect")
	}

	// maker signs + posts an OBX/XNO offer (PostOffer gossips it to nodeB).
	makerSec := commit.HashToScalar([]byte("p2p/prov/maker"), []byte("seed"))
	makerPub := new(edwards25519.Point).ScalarBaseMult(makerSec).Bytes()
	o := &swapbook.Offer{
		GiveAsset: "OBX", GetAsset: "XNO",
		GiveAmount: 5, GetAmount: 5,
		Expiry: time.Now().Add(time.Hour).Unix(),
	}
	o.Sign(makerSec)
	if err := nodeA.PostOffer(o); err != nil {
		t.Fatalf("PostOffer: %v", err)
	}

	// nodeB should learn the offer AND record nodeA as the provenance peer for makerPub.
	deadline = time.Now().Add(15 * time.Second)
	var peer string
	var ok bool
	for time.Now().Before(deadline) {
		if peer, ok = nodeB.PeerForMaker(makerPub); ok {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ok || peer == "" {
		t.Fatalf("taker did not record offer provenance for maker %s", hex.EncodeToString(makerPub))
	}
	// the recorded peer must be one of nodeB's connected peers (the relayer of the offer).
	found := false
	for _, a := range nodeB.PeerAddrs() {
		if a == peer {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("provenance peer %q is not a connected peer of the taker (%v)", peer, nodeB.PeerAddrs())
	}

	// an unknown maker must resolve to not-found.
	if _, ok := nodeB.PeerForMaker(make([]byte, 32)); ok {
		t.Fatal("PeerForMaker returned ok for an unknown maker")
	}
}
