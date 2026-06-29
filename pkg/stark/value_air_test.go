package stark

import "testing"

func TestValueBalanceHonest(t *testing.T) {
	cases := []struct{ in, out, fee uint64 }{
		{100, 90, 10}, {1, 0, 1}, {1 << 40, (1 << 40) - 5, 5}, {12345678, 12340678, 5000},
	}
	for _, c := range cases {
		pf, err := ProveValueBalance(Felt(c.in), Felt(c.out), Felt(c.fee), 48, airQueries)
		if err != nil {
			t.Fatalf("%+v prove: %v", c, err)
		}
		if !VerifyValueBalance(Felt(c.fee), 48, pf, airQueries) {
			t.Fatalf("%+v honest balance rejected", c)
		}
	}
}

// TestValueBalanceWrongSum: a_in ≠ a_out + fee cannot be proven (in-circuit balance).
func TestValueBalanceWrongSum(t *testing.T) {
	// claim 100 in, 80 out, fee 10 → 80+10=90 ≠ 100: unprovable.
	if _, err := ProveValueBalance(Felt(100), Felt(80), Felt(10), 48, airQueries); err != errAIRBadTrace {
		t.Fatalf("expected errAIRBadTrace for imbalance, got %v", err)
	}
}

// TestValueBalanceNoInflationViaWrap: trying to "create" value by making a_out wrap
// (a_out = field-huge so a_out+fee aliases to a_in) is blocked by the range proof.
func TestValueBalanceNoInflationViaWrap(t *testing.T) {
	// a_out = P - 1 (huge), fee = 2, a_in = 1 (since (P-1)+2 ≡ 1 mod P). Balance holds
	// in-field but a_out is out of range ⇒ rejected.
	if _, err := ProveValueBalance(Felt(1), Felt(PModulus-1), Felt(2), 48, airQueries); err != errAIRBadTrace {
		t.Fatalf("expected errAIRBadTrace for wrap-inflation, got %v", err)
	}
}

// TestValueBalanceWrongFee: verifying under a different public fee fails.
func TestValueBalanceWrongFee(t *testing.T) {
	pf, _ := ProveValueBalance(Felt(100), Felt(90), Felt(10), 48, airQueries)
	if VerifyValueBalance(Felt(11), 48, pf, airQueries) {
		t.Fatal("balance accepted under wrong fee")
	}
}
