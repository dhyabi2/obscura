package anonymity

import (
	"testing"

	"filippo.io/edwards25519"

	"obscura/pkg/commit"
)

// buildRings makes ownRing = {x_i·G} and the amount commitments C_i = Commit(v_i,r_i).
func buildRings(n int) (own []*edwards25519.Point, secs []*edwards25519.Scalar, amt []*edwards25519.Point, vals []uint64, blinds []*edwards25519.Scalar) {
	own = make([]*edwards25519.Point, n)
	secs = make([]*edwards25519.Scalar, n)
	amt = make([]*edwards25519.Point, n)
	vals = make([]uint64, n)
	blinds = make([]*edwards25519.Scalar, n)
	for i := 0; i < n; i++ {
		secs[i] = commit.RandomScalar()
		own[i] = new(edwards25519.Point).ScalarBaseMult(secs[i])
		vals[i] = uint64(100 + i*10)
		blinds[i] = commit.RandomScalar()
		amt[i] = commit.Commit(vals[i], blinds[i])
	}
	return
}

func valRingFor(amt []*edwards25519.Point, cprime *edwards25519.Point) []*edwards25519.Point {
	out := make([]*edwards25519.Point, len(amt))
	for i := range amt {
		out[i] = new(edwards25519.Point).Subtract(amt[i], cprime)
	}
	return out
}

// TestAnonSpendHonest: an honest anonymous spend (own coin l, pseudo-commitment
// to the SAME value) verifies — hiding which coin.
func TestAnonSpendHonest(t *testing.T) {
	for _, n := range []int{2, 4, 8} {
		own, secs, amt, vals, blinds := buildRings(n)
		for l := 0; l < n; l++ {
			rp := commit.RandomScalar()
			cprime := commit.Commit(vals[l], rp) // same value as coin l
			d := new(edwards25519.Scalar).Subtract(blinds[l], rp)
			val := valRingFor(amt, cprime)
			pf, tag, err := commit.ProveAnonSpend(own, val, l, secs[l], d, []byte("tx"))
			if err != nil {
				t.Fatalf("n=%d l=%d prove: %v", n, l, err)
			}
			if !commit.VerifyAnonSpend(own, val, tag, pf, []byte("tx")) {
				t.Fatalf("n=%d l=%d honest anon spend rejected", n, l)
			}
		}
	}
}

// TestAnonSpendInflationRejected: the spender owns coin l (knows its key) but
// tries to spend the VALUE of a different, larger coin l2. The shared-index
// joint proof must reject this (no inflation), without revealing any index.
func TestAnonSpendInflationRejected(t *testing.T) {
	own, secs, amt, vals, blinds := buildRings(8)
	l := 2  // owned coin (value 120)
	l2 := 7 // larger coin (value 170) whose value the attacker wants
	_ = blinds
	rp := commit.RandomScalar()
	cprime := commit.Commit(vals[l2], rp) // pseudo-commit to the LARGER value
	// attacker uses its real ownership secret for l, and tries every plausible d
	d := new(edwards25519.Scalar).Subtract(blinds[l], rp)
	val := valRingFor(amt, cprime)
	pf, tag, err := commit.ProveAnonSpend(own, val, l, secs[l], d, []byte("tx"))
	if err != nil {
		t.Fatalf("prove: %v", err)
	}
	if commit.VerifyAnonSpend(own, val, tag, pf, []byte("tx")) {
		t.Fatal("INFLATION: spent coin l's ownership but matched a larger coin's value")
	}
}

// TestAnonSpendWrongSecretRejected: cannot spend a coin you don't own.
func TestAnonSpendWrongSecretRejected(t *testing.T) {
	own, _, amt, vals, blinds := buildRings(8)
	l := 3
	rp := commit.RandomScalar()
	cprime := commit.Commit(vals[l], rp)
	d := new(edwards25519.Scalar).Subtract(blinds[l], rp)
	val := valRingFor(amt, cprime)
	wrong := commit.RandomScalar() // not the opening of own[l]
	pf, tag, _ := commit.ProveAnonSpend(own, val, l, wrong, d, []byte("tx"))
	if commit.VerifyAnonSpend(own, val, tag, pf, []byte("tx")) {
		t.Fatal("SOUNDNESS: anon spend accepted with wrong ownership secret")
	}
}

// TestAnonSpendForgedTagRejected: substituting the key-image tag fails.
func TestAnonSpendForgedTagRejected(t *testing.T) {
	own, secs, amt, vals, blinds := buildRings(8)
	l := 1
	rp := commit.RandomScalar()
	cprime := commit.Commit(vals[l], rp)
	d := new(edwards25519.Scalar).Subtract(blinds[l], rp)
	val := valRingFor(amt, cprime)
	pf, _, _ := commit.ProveAnonSpend(own, val, l, secs[l], d, []byte("tx"))
	forged := new(edwards25519.Point).ScalarMult(commit.RandomScalar(), commit.U())
	if commit.VerifyAnonSpend(own, val, forged, pf, []byte("tx")) {
		t.Fatal("forged key-image tag accepted")
	}
}

// TestAnonSpendTagLinksToOwner: the tag is determined by the ownership secret
// (same coin → same tag, enabling double-spend detection) and is independent of
// the pseudo-commitment / value.
func TestAnonSpendTagLinksToOwner(t *testing.T) {
	own, secs, amt, vals, blinds := buildRings(8)
	l := 4
	rp1 := commit.RandomScalar()
	rp2 := commit.RandomScalar()
	c1 := commit.Commit(vals[l], rp1)
	c2 := commit.Commit(vals[l], rp2)
	d1 := new(edwards25519.Scalar).Subtract(blinds[l], rp1)
	d2 := new(edwards25519.Scalar).Subtract(blinds[l], rp2)
	_, tag1, _ := commit.ProveAnonSpend(own, valRingFor(amt, c1), l, secs[l], d1, []byte("a"))
	_, tag2, _ := commit.ProveAnonSpend(own, valRingFor(amt, c2), l, secs[l], d2, []byte("b"))
	if tag1.Equal(tag2) != 1 {
		t.Fatal("same coin produced different tags (double-spend undetectable)")
	}
}
