package group

import (
	"crypto/rand"
	"math/big"
	"testing"
)

// naiveExpBinary is the previous LSB-first square-and-multiply, kept as the consensus
// reference: the windowed Exp must produce a byte-identical reduced form for every input.
func naiveExpBinary(G *ClassGroup, a Element, e *big.Int) Element {
	base := a.(*Form)
	exp := e
	if e.Sign() < 0 {
		base = G.Inverse(a).(*Form)
		exp = new(big.Int).Neg(e)
	}
	result := G.Identity().(*Form)
	b := base.clone()
	for i := 0; i < exp.BitLen(); i++ {
		if exp.Bit(i) == 1 {
			result = G.compose(result, b)
		}
		b = G.compose(b, b)
	}
	return result
}

// TestExpWindowedMatchesBinary pins the windowed Exp against the naive binary method on
// the production (2048-bit) and a fast (256-bit) discriminant, across edge cases, random
// exponents of many bit-lengths, negative exponents, and a 20-prime "batch product"
// exponent. Any divergence here would fork the chain (the accumulator value is in the header).
func TestExpWindowedMatchesBinary(t *testing.T) {
	for _, bits := range []int{256, 2048} {
		D := DeriveDiscriminant([]byte("obscura-mainnet-v1"), bits)
		G, err := NewClassGroup(D, "expmatch")
		if err != nil {
			t.Fatalf("NewClassGroup(%d): %v", bits, err)
		}
		g := G.Generator()
		base2 := naiveExpBinary(G, g, new(big.Int).SetUint64(0x9e3779b97f4a7c15))

		exps := []*big.Int{
			big.NewInt(0), big.NewInt(1), big.NewInt(2), big.NewInt(3),
			big.NewInt(15), big.NewInt(16), big.NewInt(17), big.NewInt(255), big.NewInt(256), big.NewInt(257),
		}
		for _, nb := range []int{1, 4, 5, 7, 31, 64, 127, 256, 512, 1024} {
			r, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), uint(nb)))
			exps = append(exps, r)
		}
		prod := big.NewInt(1)
		for i := 0; i < 20; i++ {
			p, _ := rand.Prime(rand.Reader, 256)
			prod.Mul(prod, p)
		}
		exps = append(exps, prod) // ~5120-bit batch-product exponent

		for _, b := range []Element{g, base2} {
			for _, e := range exps {
				if !G.Equal(G.Exp(b, e), naiveExpBinary(G, b, e)) {
					t.Fatalf("bits=%d e.bitlen=%d: windowed Exp != naive binary", bits, e.BitLen())
				}
				ne := new(big.Int).Neg(e)
				if !G.Equal(G.Exp(b, ne), naiveExpBinary(G, b, ne)) {
					t.Fatalf("bits=%d e.bitlen=%d (negative): windowed Exp != naive binary", bits, e.BitLen())
				}
			}
		}
	}
}

func benchExp(b *testing.B, expBits int, windowed bool) {
	D := DeriveDiscriminant([]byte("obscura-mainnet-v1"), 2048)
	G, _ := NewClassGroup(D, "bench")
	g := G.Generator()
	e, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), uint(expBits)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if windowed {
			G.Exp(g, e)
		} else {
			naiveExpBinary(G, g, e)
		}
	}
}

func BenchmarkExpWindowed256(b *testing.B)  { benchExp(b, 256, true) }
func BenchmarkExpNaive256(b *testing.B)     { benchExp(b, 256, false) }
func BenchmarkExpWindowed5120(b *testing.B) { benchExp(b, 5120, true) }
func BenchmarkExpNaive5120(b *testing.B)    { benchExp(b, 5120, false) }
