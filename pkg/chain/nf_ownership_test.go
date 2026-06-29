package chain_test

import (
	"bytes"
	"testing"

	"obscura/pkg/chain"
	"obscura/pkg/config"
	"obscura/pkg/stark"
	"obscura/pkg/tx"
	"obscura/pkg/wallet"
)

// TestNfRecipientSecretOwnership is the security regression for the CRITICAL audit
// finding "live ZK spend has no ownership binding — the sender can steal/burn any ZK
// coin it paid, and link the recipient's later spend." It drives a real chain and
// proves the recipient-secret-nullifier fix holds end to end:
//
//  1. RECIPIENT CAN SPEND: A pays a ZK coin to B; B (with B's seed) recovers and spends it.
//  2. SENDER CANNOT STEAL: A keeps everything it knows — the note, its (rho, blind, amount),
//     the PUBLIC Merkle path, and B's public address — but NOT B's nsk. Every spend A can
//     build (using its own nsk, the only one it has) is rejected: the prover refuses or
//     consensus rejects. The coin is unspendable by A.
//  3. UNLINKABILITY: A cannot precompute the nullifier B reveals (nf = H(nsk_B, rho); A
//     knows rho but not nsk_B), so A's best guess differs from the on-chain nf.
//  4. DOUBLE-SPEND: re-revealing a nullifier already in the set is rejected.
func TestNfRecipientSecretOwnership(t *testing.T) {
	old := config.CoinbaseMaturity
	config.CoinbaseMaturity = 1
	defer func() { config.CoinbaseMaturity = old }()

	c, err := chain.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	alice := wallet.FromSeed([]byte("nf-alice-seed-00000000000000000000"))
	bob := wallet.FromSeed([]byte("nf-bob-seed-0000000000000000000000"))
	carol := wallet.FromSeed([]byte("nf-carol-seed-0000000000000000000"))

	// fund Alice.
	for i := 0; i < 4; i++ {
		cb, e := alice.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(0, nil), nil)
		if e != nil {
			t.Fatal(e)
		}
		mineBlock(t, c, cb, nil)
	}
	for h := uint64(0); h <= c.Height(); h++ {
		b, _ := c.BlockByHeight(h)
		alice.ScanBlock(b)
	}
	if alice.Balance() == 0 {
		t.Fatal("alice unfunded")
	}

	fee := uint64(5_000_000_000)
	mintAmount := alice.Balance() / 4

	// Alice mints a ZK coin payable to BOB (a ZK→ZK transfer). aCoin is ALICE's view of
	// the coin: it carries the real (rho, blind, amount, leaf) but ALICE's nsk — which does
	// NOT match the note's pk = B.NfPk. This is exactly the sender's privileged knowledge.
	mintTx, aCoin, err := alice.CreateZKMintTo(c, bob.Address(), mintAmount, fee)
	if err != nil {
		t.Fatalf("mint to bob: %v", err)
	}
	cb, _ := alice.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(mintTx.Fee, nil), nil)
	mineBlock(t, c, cb, []*tx.Transaction{mintTx})

	// --- (1) RECIPIENT CAN SPEND ---
	var bCoin *wallet.ZKCoin
	for h := uint64(0); h <= c.Height(); h++ {
		b, _ := c.BlockByHeight(h)
		for _, zc := range bob.ScanZKCoins(b) {
			bCoin = zc
		}
	}
	if bCoin == nil {
		t.Fatal("bob did not recover the coin Alice paid him")
	}
	if bCoin.Amount != mintAmount {
		t.Fatalf("bob recovered amount %d, want %d", bCoin.Amount, mintAmount)
	}
	// Alice scanning the same blocks must find NOTHING spendable: the note's cm reproduces
	// only under B's pk, and the stealth one-time key is B's — Alice cannot even recognize it.
	for h := uint64(0); h <= c.Height(); h++ {
		b, _ := c.BlockByHeight(h)
		if got := alice.ScanZKCoins(b); len(got) != 0 {
			t.Fatalf("alice (the sender) recognized %d ZK coins as her own — must be 0", len(got))
		}
	}

	// --- (2) SENDER CANNOT STEAL ---
	anchor, path, ok := c.ZKWitnessFor(aCoin.Leaf)
	if !ok {
		t.Fatal("minted leaf not in tree")
	}
	// Attempt A: spend with Alice's own (wrong) nsk. The circuit recomputes the note's
	// cm = sponge(H(nsk,0), amount, rho, blind); with Alice's nsk this != the committed leaf
	// (which used B's pk), so the prover refuses (bad trace). If it somehow builds a proof,
	// consensus MUST reject it.
	if theftTx, perr := alice.CreateCZKSpend(aCoin, anchor, path, c.ZKDepth(), alice.Address(), fee); perr == nil {
		cbT, _ := alice.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(theftTx.Fee, nil), nil)
		if mineMayFail(t, c, cbT, []*tx.Transaction{theftTx}) == nil {
			t.Fatal("SENDER STOLE THE COIN: a spend built with the sender's nsk was accepted")
		}
	}
	// Attempt B: spend with an arbitrary WRONG nsk the attacker might try.
	forged := *aCoin
	forged.Nsk = stark.NewFelt(0xDEADBEEF)
	if theftTx, perr := alice.CreateCZKSpend(&forged, anchor, path, c.ZKDepth(), alice.Address(), fee); perr == nil {
		cbT, _ := alice.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(theftTx.Fee, nil), nil)
		if mineMayFail(t, c, cbT, []*tx.Transaction{theftTx}) == nil {
			t.Fatal("SENDER STOLE THE COIN: a spend with a forged nsk was accepted")
		}
	}

	// --- (1 cont.) Bob spends his coin to Carol (proves the RIGHT nsk works) ---
	banchor, bpath, ok := c.ZKWitnessFor(bCoin.Leaf)
	if !ok {
		t.Fatal("bob's leaf not in tree")
	}
	bobTx, err := bob.CreateCZKSpend(bCoin, banchor, bpath, c.ZKDepth(), carol.Address(), fee)
	if err != nil {
		t.Fatalf("bob legitimate spend failed: %v", err)
	}

	// --- (3) UNLINKABILITY: Alice cannot precompute the nf Bob reveals ---
	realNf := bobTx.CZKSpends[0].Nullifier
	// Alice's best guess: she knows the coin's rho (== aCoin.Rho == bCoin.Rho) and her own
	// nsk, but not Bob's. nf = H(nsk_B, rho) != H(nsk_A, rho).
	aliceGuess := stark.NodeBytes(stark.NfNullifier(aCoin.Nsk, aCoin.Rho))
	if bytes.Equal(aliceGuess, realNf) {
		t.Fatal("LINKABILITY: the sender precomputed the nullifier the recipient revealed")
	}

	// mine Bob's legitimate spend; Carol must receive it.
	cb2, _ := alice.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(bobTx.Fee, nil), nil)
	mineBlock(t, c, cb2, []*tx.Transaction{bobTx})
	var carolGot bool
	for h := uint64(0); h <= c.Height(); h++ {
		b, _ := c.BlockByHeight(h)
		for _, zc := range carol.ScanZKCoins(b) {
			if zc.Amount == bCoin.Amount-fee {
				carolGot = true
			}
		}
	}
	if !carolGot {
		t.Fatal("carol did not receive Bob's confidential payment")
	}

	// --- (4) DOUBLE-SPEND: re-spend Bob's coin (same nf already in the set) → rejected ---
	dsTx, err := bob.CreateCZKSpend(bCoin, banchor, bpath, c.ZKDepth(), carol.Address(), fee)
	if err != nil {
		t.Fatalf("build double-spend: %v", err)
	}
	if !bytes.Equal(dsTx.CZKSpends[0].Nullifier, realNf) {
		t.Fatal("nullifier is not deterministic for the same coin — double-spend set would miss it")
	}
	cb3, _ := alice.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(dsTx.Fee, nil), nil)
	if mineMayFail(t, c, cb3, []*tx.Transaction{dsTx}) == nil {
		t.Fatal("DOUBLE-SPEND accepted: a reused nullifier was not rejected")
	}
}
