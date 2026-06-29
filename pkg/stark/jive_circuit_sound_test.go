package stark

import "testing"

// The OLD truncation root (perm output cols[0:4] at the root row) must NO LONGER be a
// valid public root — only the Jive output is. This pins that the in-circuit fix actually
// changed the bound value (and that a prover cannot pass off the truncation as the root).
func TestJiveRootIsNotTruncation(t *testing.T) {
	serial, amount, blind := Felt(7), Felt(8), Felt(9)
	tr := spend256Trace(serial, amount, blind, MerklePath256{}, 0)
	rootRow := merkleBlock*2 - 1
	trunc := Node256{tr[0][rootRow], tr[1][rootRow], tr[2][rootRow], tr[3][rootRow]}
	var jive Node256 // Jive output = fold(13..16) + perm out (0..3) + (4..7)
	for i := 0; i < 4; i++ {
		jive[i] = tr[13+i][rootRow].Add(tr[i][rootRow]).Add(tr[4+i][rootRow])
	}
	if trunc == jive {
		t.Fatal("Jive output equals truncation — feed-forward not applied")
	}
	if jive != SpendLeaf256(serial, amount, blind) {
		t.Fatal("Jive rootOut != native SpendLeaf256")
	}
	// proving against the truncation as the public root must FAIL (it's not the real leaf).
	if _, err := ProveSpend256(serial, amount, blind, MerklePath256{}, 0, trunc, nil, airQueries); err != errAIRBadTrace {
		t.Fatalf("truncation-as-root accepted (err=%v) — unsound", err)
	}
	// the genuine Jive leaf as root must succeed.
	pf, err := ProveSpend256(serial, amount, blind, MerklePath256{}, 0, jive, nil, airQueries)
	if err != nil || !VerifySpend256(serial, amount, jive, nil, 0, pf, airQueries) {
		t.Fatalf("genuine Jive root rejected: err=%v", err)
	}
}

// Tampering a Jive-fold column must break the proof (the f-carry is fully bound).
func TestJiveFoldColumnBound(t *testing.T) {
	serial, amount, blind := Felt(3), Felt(4), Felt(5)
	imt := buildSpend256Tree(3, 1, serial, amount, blind, 9)
	c := spend256Circuit{depth: 3, serial: serial, amount: amount, root: imt.Root(), bind: nil}
	tr := spend256Trace(serial, amount, blind, imt.PathFor(1), 3)
	// corrupt an f column mid-block — must violate a constraint (ProveAIR rejects).
	tr[13][5] = tr[13][5].Add(1)
	if _, err := ProveAIR(c, tr, airQueries); err != errAIRBadTrace {
		t.Fatalf("tampered f-column accepted (err=%v) — unsound carry", err)
	}
}
