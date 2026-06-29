package stark

import "testing"

func TestMint256Honest(t *testing.T) {
	serial, amount, blind := Felt(0x11), Felt(5000), Felt(0x22)
	leaf := SpendLeaf256(serial, amount, blind)
	pf, err := ProveMint256(serial, amount, blind, nil, airQueries)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyMint256(leaf, amount, nil, pf, airQueries) {
		t.Fatal("honest wide mint rejected")
	}
}

func TestMint256ForgedAmount(t *testing.T) {
	serial, amount, blind := Felt(0x11), Felt(5000), Felt(0x22)
	leaf := SpendLeaf256(serial, amount, blind)
	pf, _ := ProveMint256(serial, amount, blind, nil, airQueries)
	if VerifyMint256(leaf, amount.Add(1), nil, pf, airQueries) {
		t.Fatal("wide mint accepted with inflated amount")
	}
}

func TestMint256Binding(t *testing.T) {
	serial, amount, blind := Felt(7), Felt(42), Felt(8)
	leaf := SpendLeaf256(serial, amount, blind)
	bindA := []byte("tx-domain-AAAA")
	bindB := []byte("tx-domain-BBBB")
	pf, _ := ProveMint256(serial, amount, blind, bindA, airQueries)
	if VerifyMint256(leaf, amount, bindB, pf, airQueries) {
		t.Fatal("wide mint verified under wrong binding")
	}
	if !VerifyMint256(leaf, amount, bindA, pf, airQueries) {
		t.Fatal("wide mint failed under correct binding")
	}
}
