package stark

import (
	"math/rand"
	"testing"
)

// AUDIT item 2: the trace builder's per-round function MUST equal the reference
// permutation, and a full block MUST equal the clear-text hash — else proofs verify
// against the wrong tree.

func TestAuditWideRoundMatchesPermute(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 5000; i++ {
		var s [poseidonWideT]Felt
		for j := range s {
			s[j] = randField(r)
		}
		// fold wideRoundStep over all rounds == poseidonWidePermute
		st := s
		for round := 0; round < poseidonWideRounds; round++ {
			st = wideRoundStep(st, round)
		}
		if st != poseidonWidePermute(s) {
			t.Fatal("wideRoundStep chain != poseidonWidePermute")
		}
	}
}

func TestAuditNarrowRoundMatchesPermute(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for i := 0; i < 5000; i++ {
		var s [poseidonT]Felt
		for j := range s {
			s[j] = randField(r)
		}
		// reproduce PoseidonPermute via per-round, compare
		st := s
		round := 0
		half := poseidonRF / 2
		step := func(full bool) {
			for k := 0; k < poseidonT; k++ {
				st[k] = st[k].Add(poseidonRC[round][k])
			}
			if full {
				for k := 0; k < poseidonT; k++ {
					st[k] = sbox(st[k])
				}
			} else {
				st[0] = sbox(st[0])
			}
			st = mds(st)
			round++
		}
		for j := 0; j < half; j++ {
			step(true)
		}
		for j := 0; j < poseidonRP; j++ {
			step(false)
		}
		for j := 0; j < half; j++ {
			step(true)
		}
		if st != PoseidonPermute(s) {
			t.Fatal("narrow per-round != PoseidonPermute")
		}
	}
}

// The width-8 spend trace's first block output[0:4] must equal WideHash2(serialNode,
// amountNode) — i.e. the trace builder computes the same hash the tree uses.
func TestAuditTraceBlockMatchesWideHash(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	for i := 0; i < 200; i++ {
		serial, amount, blind := randField(r), randField(r), randField(r)
		// the clear-text leaf
		want := SpendLeaf256(serial, amount, blind)
		// build a depth-0 trace and read the leaf at the last row (block 1 output)
		cols := spend256Trace(serial, amount, blind, MerklePath256{}, 0)
		rootRow := merkleBlock*2 - 1
		// The leaf is the JIVE compression output = fold(cols[13..16]) + perm output
		// cols[0:4] + cols[4:8] at the root row — what the periodic root-binding enforces —
		// NOT the truncation cols[0..3].
		var got Node256
		for i := 0; i < 4; i++ {
			got[i] = cols[13+i][rootRow].Add(cols[i][rootRow]).Add(cols[4+i][rootRow])
		}
		if got != want {
			t.Fatal("spend256 trace leaf != clear-text SpendLeaf256")
		}
	}
}
