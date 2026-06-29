// Package dandelion tests that Dandelion++ tx-origin privacy (Block 4) still
// propagates transactions network-wide (stem → fluff → fail-safe liveness),
// i.e. privacy routing does not black-hole transactions.
package dandelion

import (
	"testing"
	"time"

	"obscura/pkg/mempool"
	"obscura/pkg/p2p"
	"obscura/tests/critical/harness"
)

func pollUntil(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return cond()
}

// TestDandelionPropagation: a tx originated through the Dandelion stem on one
// node still reaches every node's mempool (liveness preserved).
func TestDandelionPropagation(t *testing.T) {
	defer harness.SmallMaturity()()
	chA := harness.NewChain(t)
	chB := harness.NewChain(t)
	chC := harness.NewChain(t)

	alice := harness.NewWallet("dand-alice")
	harness.MineN(t, chA, alice, 4)
	harness.ScanAll(chA, alice)

	mpA := mempool.New(chA)
	mpB := mempool.New(chB)
	mpC := mempool.New(chC)
	nodeA := p2p.NewNode("127.0.0.1:19651", chA, mpA, "")
	nodeB := p2p.NewNode("127.0.0.1:19652", chB, mpB, "")
	nodeC := p2p.NewNode("127.0.0.1:19653", chC, mpC, "")
	defer nodeA.Stop()
	defer nodeB.Stop()
	defer nodeC.Stop()
	if err := nodeA.Start(nil); err != nil {
		t.Fatal(err)
	}
	if err := nodeB.Start([]string{"127.0.0.1:19651"}); err != nil { // B -> A
		t.Fatal(err)
	}
	if err := nodeC.Start([]string{"127.0.0.1:19652"}); err != nil { // C -> B
		t.Fatal(err)
	}

	// all chains sync to A's height
	if !pollUntil(40*time.Second, func() bool {
		return chB.Height() == chA.Height() && chC.Height() == chA.Height()
	}) {
		t.Fatalf("sync failed: A=%d B=%d C=%d", chA.Height(), chB.Height(), chC.Height())
	}

	// build a valid tx and originate it via Dandelion on node C (which has an
	// outbound stem peer = B).
	bob := harness.NewWallet("dand-bob")
	fee := uint64(1_000_000_000)
	spend, err := alice.CreateTransaction(chC, bob.Address(), alice.Balance()/8, fee)
	if err != nil {
		t.Fatalf("create tx: %v", err)
	}
	if err := mpC.Add(spend); err != nil {
		t.Fatalf("seed origin mempool: %v", err)
	}
	nodeC.BroadcastTx(spend) // enters the stem phase

	// despite private stem routing, the tx must reach every node's mempool
	if !pollUntil(40*time.Second, func() bool {
		return mpA.Size() >= 1 && mpB.Size() >= 1
	}) {
		t.Fatalf("dandelion did not propagate tx: mpA=%d mpB=%d mpC=%d", mpA.Size(), mpB.Size(), mpC.Size())
	}
}
