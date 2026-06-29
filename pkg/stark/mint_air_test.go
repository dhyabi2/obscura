package stark

import "testing"

func TestMintHonest(t *testing.T) {
	serial, amount, blind := Felt(0x11), Felt(5000), Felt(0x22)
	leaf := SpendLeaf(serial, amount, blind)
	pf, err := ProveMint(serial, amount, blind, 0, airQueries)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyMint(leaf, amount, 0, pf, airQueries) {
		t.Fatal("honest mint rejected")
	}
}

// TestMintForgedAmount: a leaf committing amount A cannot be passed off as amount B
// — the anti-inflation property at mint time.
func TestMintForgedAmount(t *testing.T) {
	serial, amount, blind := Felt(0x11), Felt(5000), Felt(0x22)
	leaf := SpendLeaf(serial, amount, blind)
	pf, _ := ProveMint(serial, amount, blind, 0, airQueries)
	if VerifyMint(leaf, amount.Add(1), 0, pf, airQueries) {
		t.Fatal("mint accepted with inflated amount claim")
	}
}

// TestMintWrongLeaf: proof for one leaf must not verify another.
func TestMintWrongLeaf(t *testing.T) {
	pf, _ := ProveMint(Felt(1), Felt(100), Felt(2), 0, airQueries)
	if VerifyMint(SpendLeaf(Felt(9), Felt(100), Felt(9)), Felt(100), 0, pf, airQueries) {
		t.Fatal("mint accepted for a different leaf")
	}
}

// TestMintBinding: a proof bound to one tx domain must fail under another.
func TestMintBinding(t *testing.T) {
	serial, amount, blind := Felt(7), Felt(42), Felt(8)
	leaf := SpendLeaf(serial, amount, blind)
	pf, _ := ProveMint(serial, amount, blind, Felt(0xAAAA), airQueries)
	if VerifyMint(leaf, amount, Felt(0xBBBB), pf, airQueries) {
		t.Fatal("mint proof verified under wrong binding")
	}
	if !VerifyMint(leaf, amount, Felt(0xAAAA), pf, airQueries) {
		t.Fatal("mint proof failed under correct binding")
	}
}
