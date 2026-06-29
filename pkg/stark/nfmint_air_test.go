package stark

import "testing"

func TestNfMintHonestAndBinding(t *testing.T) {
	pk := NfAddress(Felt(0x2222))
	amount, rho, blind := Felt(1_000_000), Felt(0xAAA), Felt(0xBBB)
	cm := NfNoteFromPk(pk, amount, rho, blind)
	pf, err := ProveNfMint(pk, amount, rho, blind, cm, nil, airQueries)
	if err != nil {
		t.Fatalf("prove: %v", err)
	}
	if !VerifyNfMint(amount, cm, nil, pf, airQueries) {
		t.Fatal("honest nf mint rejected")
	}
	// anti-inflation: same cm cannot verify against a larger declared amount.
	if VerifyNfMint(amount+1, cm, nil, pf, airQueries) {
		t.Fatal("mint verified under a different amount (inflation)")
	}
	// a cm that commits a different amount than declared cannot be proven.
	wrong := NfNoteFromPk(pk, amount+5, rho, blind)
	if _, err := ProveNfMint(pk, amount, rho, blind, wrong, nil, airQueries); err != errAIRBadTrace {
		t.Fatalf("expected errAIRBadTrace for mismatched cm, got %v", err)
	}
}

func TestNfMintPrivacy(t *testing.T) {
	pk := NfAddress(Felt(0x7777))
	amount, rho, blind := Felt(42), Felt(0xCAFE), Felt(0xF00D)
	cm := NfNoteFromPk(pk, amount, rho, blind)
	pf, _ := ProveNfMint(pk, amount, rho, blind, cm, nil, airQueries)
	revealed := flattenExt(nil, pf.Fz...)
	revealed = flattenExt(revealed, pf.Fgz...)
	for q := range pf.OpenP {
		revealed = append(revealed, pf.OpenP[q].Cols...)
		revealed = append(revealed, pf.OpenS[q].Cols...)
	}
	for _, x := range revealed {
		if x == rho || x == blind || x == pk[0] {
			t.Fatal("a hidden mint witness (pk/rho/blind) leaked")
		}
	}
}
