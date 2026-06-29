package stark

import (
	"math/rand"
	"testing"
)

const friQueries = 40 // ~80-bit soundness at rate 1/4 (per-query error ≈ 5/8)

func randCoeffs(r *rand.Rand, n int) []Felt {
	c := make([]Felt, n)
	for i := range c {
		c[i] = randField(r)
	}
	return c
}

// TestFRIHonest: a genuine degree-<d polynomial passes.
func TestFRIHonest(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for _, d := range []int{2, 4, 8, 16, 64, 256} {
		pf := ProveFRI(randCoeffs(r, d), friQueries)
		if !VerifyFRI(pf, friQueries) {
			t.Fatalf("honest degree-%d proof rejected", d)
		}
	}
}

// TestFRIHighDegreeRejected: a random function over the domain (NOT low-degree)
// must be rejected — the central soundness property.
func TestFRIHighDegreeRejected(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	d := 64
	N0 := friBlowup * d
	rejected := 0
	trials := 20
	for i := 0; i < trials; i++ {
		eval0 := randCoeffs(r, N0) // random evals ⇒ degree ≈ N0 ≫ d
		pf := proveFRIEvals(eval0, d, friQueries)
		if !VerifyFRI(pf, friQueries) {
			rejected++
		}
	}
	if rejected != trials {
		t.Fatalf("high-degree function accepted in %d/%d trials", trials-rejected, trials)
	}
}

// TestFRIBarelyTooHighRejected: degree exactly d (one over the bound d, i.e. not
// < d) must be rejected. This is the tight boundary case.
func TestFRIBarelyTooHighRejected(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	d := 64
	N0 := friBlowup * d
	coeffs := make([]Felt, N0)
	for i := 0; i <= d; i++ { // degree exactly d (coeff at index d nonzero)
		coeffs[i] = randField(r)
	}
	eval0 := NTT(coeffs)
	pf := proveFRIEvals(eval0, d, friQueries)
	if VerifyFRI(pf, friQueries) {
		t.Fatal("degree-d (not < d) function accepted")
	}
}

// TestFRITamperedValue: flipping one opened evaluation breaks Merkle or fold.
func TestFRITamperedValue(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	pf := ProveFRI(randCoeffs(r, 64), friQueries)
	pf.Queries[0].Layers[0].X = pf.Queries[0].Layers[0].X.Add(1)
	if VerifyFRI(pf, friQueries) {
		t.Fatal("tampered opened value accepted")
	}
}

// TestFRITamperedFinal: changing the claimed constant must fail.
func TestFRITamperedFinal(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	pf := ProveFRI(randCoeffs(r, 64), friQueries)
	pf.FinalValue = pf.FinalValue.Add(1)
	if VerifyFRI(pf, friQueries) {
		t.Fatal("tampered final value accepted")
	}
}

// TestFRITamperedRoot: corrupting a layer root must fail (transcript diverges and
// Merkle openings stop matching).
func TestFRITamperedRoot(t *testing.T) {
	r := rand.New(rand.NewSource(6))
	pf := ProveFRI(randCoeffs(r, 64), friQueries)
	pf.Roots[1][0] ^= 0xFF
	if VerifyFRI(pf, friQueries) {
		t.Fatal("tampered root accepted")
	}
}

// TestFRIWrongDegreeClaim: claiming a smaller degree bound than the real one fails.
func TestFRIWrongDegreeClaim(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	pf := ProveFRI(randCoeffs(r, 64), friQueries)
	pf.Degree = 32 // lie: claim degree < 32 for a degree-<64 poly
	if VerifyFRI(pf, friQueries) {
		t.Fatal("shrunken degree claim accepted")
	}
}

// TestFRIForgedTranscript: a prover cannot pick query positions; rewriting Pos to
// dodge a bad layer must fail because the verifier re-derives positions.
func TestFRIForgedTranscript(t *testing.T) {
	r := rand.New(rand.NewSource(8))
	pf := ProveFRI(randCoeffs(r, 64), friQueries)
	pf.Queries[0].Pos = (pf.Queries[0].Pos + 1) % (friBlowup * 64 / 2)
	if VerifyFRI(pf, friQueries) {
		t.Fatal("forged query position accepted")
	}
}

// TestFRIGrindTampered: corrupting the grinding nonce must fail verification.
func TestFRIGrindTampered(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	pf := ProveFRI(randCoeffs(r, 64), friQueries)
	pf.Grind ^= 0xFFFF
	if VerifyFRI(pf, friQueries) {
		t.Fatal("tampered grinding nonce accepted")
	}
}

// TestFRIGrindMeetsDifficulty: an honest proof's nonce really meets the difficulty.
func TestFRIGrindMeetsDifficulty(t *testing.T) {
	r := rand.New(rand.NewSource(12))
	pf := ProveFRI(randCoeffs(r, 32), friQueries)
	if !VerifyFRI(pf, friQueries) {
		t.Fatal("honest grind proof rejected")
	}
}
