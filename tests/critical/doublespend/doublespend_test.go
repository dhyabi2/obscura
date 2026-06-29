// Package doublespend is the regression for the CRITICAL audit finding that the
// transparent UTXO spent-set and the anonymous key-image set were unlinked, so a
// coin could be spent ONCE transparently AND ONCE anonymously (double its value).
// The fix unifies the nullifier: a transparent spend now publishes the same
// key-image an anonymous spend would, checked against one shared set.
package doublespend

import (
	"strings"
	"testing"

	"obscura/pkg/commit"
	"obscura/pkg/config"
	"obscura/pkg/tx"
	"obscura/pkg/wallet"
	"obscura/tests/critical/harness"
)

const fee = 100_000_000

func TestAnonThenTransparentRejected(t *testing.T) {
	defer harness.SmallMaturity()()
	// small anonymity pool keeps the ring proof (and mining) fast in this test.
	oldPool := config.PoolSize
	config.PoolSize = 4
	defer func() { config.PoolSize = oldPool }()

	c := harness.NewChain(t)
	alice := harness.NewWallet("ds-alice")
	harness.Funded(t, c, alice, 6) // > PoolSize so pool 0 is complete & mature

	poolKeys, poolCommits, ok := c.PoolMembers(0, c.Height()+1)
	if !ok {
		t.Fatal("pool 0 not ready")
	}
	bob := harness.NewWallet("ds-bob")

	// 1) spend a coin ANONYMOUSLY → records its key-image T in the shared set.
	anonTx, err := alice.CreateAnonTransaction(c, 0, poolKeys, poolCommits, bob.Address(), 1_000_000_000, fee)
	if err != nil {
		t.Fatalf("anon tx: %v", err)
	}
	harness.MineBlock(t, c, harness.NewWallet("ds-sink"), []*tx.Transaction{anonTx})
	tag := anonTx.AnonInputs[0].Tag

	// identify which owned coin C was spent (its key-image == tag)
	var spent *wallet.OwnedOutput
	for _, o := range alice.Outputs {
		if string(commit.KeyImage(o.OneTime).Bytes()) == string(tag) {
			spent = o
			break
		}
	}
	if spent == nil {
		t.Fatal("could not identify the anonymously-spent coin")
	}

	// 2) attempt to ALSO spend coin C transparently. Force the wallet to select C
	// by hiding every other output (anon spend left C unmarked in the wallet).
	saved := make([]bool, len(alice.Outputs))
	for i, o := range alice.Outputs {
		saved[i] = o.Spent
		if o != spent {
			o.Spent = true
		}
	}
	spent.ReleaseReserved() // anon spend reserved C; free it so the build can pick it
	transparentTx, err := alice.CreateTransaction(c, bob.Address(), 500_000_000, fee)
	for i, o := range alice.Outputs { // restore
		o.Spent = saved[i]
	}
	if err != nil {
		t.Fatalf("build transparent spend of C: %v", err)
	}
	// it must spend exactly C
	if string(transparentTx.Inputs[0].OutputRef) != string(spent.Out.OneTimeKey) {
		t.Fatal("transparent tx did not select the target coin")
	}

	// 3) the transparent spend of an already-anonymously-spent coin MUST be rejected.
	err = c.ValidateStandaloneTx(transparentTx)
	if err == nil {
		t.Fatal("CRITICAL: transparent spend of an anonymously-spent coin was ACCEPTED (double-spend)")
	}
	if !strings.Contains(err.Error(), "key-image") {
		t.Fatalf("rejected, but not for the key-image reason: %v", err)
	}
}
