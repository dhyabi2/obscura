package stark

import "testing"

// TestProofRoundTrip: a proof survives marshal→unmarshal and still verifies.
func TestProofRoundTrip(t *testing.T) {
	serial, amount, blind := Felt(0x1111), Felt(0x2222), Felt(0x3333)
	idx, depth := 3, 4
	m := buildSpendTree(depth, idx, serial, amount, blind, 17)
	pf, err := ProveSpend(serial, amount, blind, m.PathFor(idx), depth, m.Root(), airQueries)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := MarshalProof(pf)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnmarshalProof(blob)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifySpend(serial, amount, m.Root(), depth, got, airQueries) {
		t.Fatal("round-tripped proof failed to verify")
	}
	t.Logf("anon-spend proof size: %d bytes (depth=%d)", len(blob), depth)
}
