package chain

import (
	"os"
	"strings"
	"testing"

	"obscura/pkg/config"
	"obscura/pkg/stark"
	"obscura/pkg/tx"
)

// goldilocksP is the Goldilocks modulus the STARK reduces amounts by.
const goldilocksP = uint64(0xFFFFFFFF00000001)

// TestZKInflationAliasRejected is the regression for the CRITICAL inflation bug
// found in audit: a ZK amount declared as a+P (a valid uint64 that aliases to a in
// the field, but counts as ~2^64 in conservation). The supply-cap range check must
// reject any amount that could alias (MoneySupplyCap < P), in BOTH directions.
func TestZKInflationAliasRejected(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// the alias of amount 5: 5 + P, a valid uint64 that reduces to 5 mod P.
	alias := uint64(5) + goldilocksP
	if alias <= config.MoneySupplyCap {
		t.Fatal("test precondition broken: alias should exceed supply cap")
	}

	// SPEND side: amount = alias must be rejected before the felt conversion.
	spendTx := &tx.Transaction{ZKInputs: []tx.ZKInput{{
		Nullifier: make([]byte, 32), Anchor: make([]byte, 32), Amount: alias,
	}}}
	if _, err := c.validateZKInputsLocked(spendTx, map[string]bool{}, true); err == nil ||
		!strings.Contains(err.Error(), "supply cap") {
		t.Fatalf("aliased spend amount not rejected by supply-cap check: %v", err)
	}

	// MINT side: same.
	mintTx := &tx.Transaction{ZKOutputs: []tx.ZKOutput{{
		Leaf: make([]byte, 32), Amount: alias,
	}}}
	if _, err := c.validateZKOutputsLocked(mintTx, true); err == nil ||
		!strings.Contains(err.Error(), "supply cap") {
		t.Fatalf("aliased mint amount not rejected by supply-cap check: %v", err)
	}

	// A just-over-cap amount (still < P, no alias) is also rejected (supply bound).
	overCap := config.MoneySupplyCap + 1
	overTx := &tx.Transaction{ZKInputs: []tx.ZKInput{{
		Nullifier: make([]byte, 32), Anchor: make([]byte, 32), Amount: overCap,
	}}}
	if _, err := c.validateZKInputsLocked(overTx, map[string]bool{}, true); err == nil {
		t.Fatal("over-supply-cap amount accepted")
	}

	// A legitimate amount (≤ cap) passes the range check (fails later on anchor, fine).
	okTx := &tx.Transaction{ZKInputs: []tx.ZKInput{{
		Nullifier: make([]byte, 32), Anchor: make([]byte, 32), Amount: 1_000_000,
	}}}
	if _, err := c.validateZKInputsLocked(okTx, map[string]bool{}, true); err == nil ||
		strings.Contains(err.Error(), "supply cap") {
		t.Fatalf("legitimate amount wrongly hit supply-cap check: %v", err)
	}
}

// TestZKNonCanonicalSerialRejected is the regression for the SECOND aliasing bug
// (double-spend): the nullifier set keys on raw serial bytes but the proof binds the
// serial reduced mod P, so serial S and S+P alias to the same coin but different
// zkNull keys. The canonical-encoding check must reject any serial/anchor/leaf whose
// bytes aren't the canonical (reduced) form.
func TestZKNonCanonicalSerialRejected(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	canonical := func(v uint64) []byte {
		b := make([]byte, 8)
		// big-endian encoding of v (canonical for v < P)
		for i := 7; i >= 0; i-- {
			b[i] = byte(v)
			v >>= 8
		}
		return b
	}
	// node32 builds a 32B Node256 from a single 8-byte big-endian chunk (rest zero).
	node32 := func(chunk0 []byte) []byte { b := make([]byte, 32); copy(b[0:8], chunk0); return b }
	// nullifier 5 (canonical) vs an alias chunk 5+P (a non-canonical Node256 chunk that
	// reduces to the same field element) — the double-spend primitive, now over the 32B nf.
	okNf := node32(canonical(5))
	badNf := node32(canonical(5 + goldilocksP))

	okTx := &tx.Transaction{ZKInputs: []tx.ZKInput{{Nullifier: okNf, Anchor: make([]byte, 32), Amount: 1000}}}
	if _, err := c.validateZKInputsLocked(okTx, map[string]bool{}, true); err != nil &&
		strings.Contains(err.Error(), "non-canonical") {
		t.Fatalf("canonical nullifier wrongly rejected: %v", err)
	}
	aliasTx := &tx.Transaction{ZKInputs: []tx.ZKInput{{Nullifier: badNf, Anchor: make([]byte, 32), Amount: 1000}}}
	if _, err := c.validateZKInputsLocked(aliasTx, map[string]bool{}, true); err == nil ||
		!strings.Contains(err.Error(), "non-canonical zk nullifier") {
		t.Fatalf("non-canonical (aliased) nullifier not rejected: %v", err)
	}

	// non-canonical anchor (one 8-byte chunk ≥ P) must be rejected.
	badAnchor := make([]byte, 32)
	copy(badAnchor[0:8], canonical(7+goldilocksP))
	naTx := &tx.Transaction{ZKInputs: []tx.ZKInput{{Nullifier: okNf, Anchor: badAnchor, Amount: 1000}}}
	if _, err := c.validateZKInputsLocked(naTx, map[string]bool{}, true); err == nil ||
		!strings.Contains(err.Error(), "non-canonical zk anchor") {
		t.Fatalf("non-canonical anchor not rejected: %v", err)
	}

	// non-canonical leaf (mint) must be rejected.
	badLeaf := make([]byte, 32)
	copy(badLeaf[0:8], canonical(9+goldilocksP))
	nlTx := &tx.Transaction{ZKOutputs: []tx.ZKOutput{{Leaf: badLeaf, Amount: 1000}}}
	if _, err := c.validateZKOutputsLocked(nlTx, true); err == nil ||
		!strings.Contains(err.Error(), "non-canonical zk leaf") {
		t.Fatalf("non-canonical leaf not rejected: %v", err)
	}
}

// TestMoneySupplyCapBelowP is the STRUCTURAL invariant that prevents the amount-
// aliasing class from ever reopening: ZK amounts are bound in-circuit mod P but used
// as raw uint64 in conservation, so the supply cap (the max accepted amount) MUST
// stay strictly below P. If someone raises MoneySupplyCap to/above P, this fails.
func TestMoneySupplyCapBelowP(t *testing.T) {
	if config.MoneySupplyCap >= stark.PModulus {
		t.Fatalf("MoneySupplyCap (%d) must be < Goldilocks P (%d) or ZK amounts can alias",
			config.MoneySupplyCap, uint64(stark.PModulus))
	}
}

// TestZKConsensusUsesStrictDecode is a STRUCTURAL guard: the ZK consensus validators
// must NOT use the silently-reducing FeltFromBytes/NodeFromBytes on untrusted tx
// input (only the strict ParseFelt/ParseNode), so the field↔bytes aliasing class
// cannot be reintroduced at a new boundary. It scans the source of the two validators.
func TestZKConsensusUsesStrictDecode(t *testing.T) {
	src, err := os.ReadFile("zkspend.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	// isolate the two validator functions
	for _, fn := range []string{"func (c *Chain) validateZKInputsLocked", "func (c *Chain) validateZKOutputsLocked"} {
		i := strings.Index(s, fn)
		if i < 0 {
			t.Fatalf("validator %q not found", fn)
		}
		body := s[i:]
		if j := strings.Index(body[1:], "\nfunc "); j >= 0 {
			body = body[:j]
		}
		for _, bad := range []string{"FeltFromBytes(in.", "NodeFromBytes(in.", "FeltFromBytes(o.", "NodeFromBytes(o."} {
			if strings.Contains(body, bad) {
				t.Fatalf("%s uses silently-reducing %s on untrusted input — use ParseFelt/ParseNode", fn, bad)
			}
		}
	}
}

// TestZKFinalizedEpochAnchorPermanent is the regression for REVIEW FINDING 1
// (fund-loss): finalized epoch terminal roots must NEVER be evicted from the anchor
// set by current-epoch window churn, else coins in old epochs become unspendable.
func TestZKFinalizedEpochAnchorPermanent(t *testing.T) {
	oldW, oldD := config.MaxAnchorWindow, stark.ZKDepth
	config.MaxAnchorWindow = 3 // tiny window → forces eviction quickly
	stark.ZKDepth = 1          // epoch cap = 2 coins
	defer func() { config.MaxAnchorWindow, stark.ZKDepth = oldW, oldD }()

	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	node := func(i uint64) stark.Node256 {
		return stark.Node256{stark.NewFelt(i + 1), stark.NewFelt(7), stark.NewFelt(9), stark.NewFelt(11)}
	}
	// fill epoch 0 (2 coins) then roll into epoch 1 → epoch 0 finalizes.
	c.cmTree.Append(node(0))
	c.recordCMAnchorLocked()
	c.cmTree.Append(node(1))
	c.recordCMAnchorLocked()
	c.cmTree.Append(node(2)) // rolls to epoch 1
	c.recordCMAnchorLocked()

	ep0root, _ := c.cmTree.RootAt(0)
	key0 := hexstr(stark.NodeBytes(ep0root))
	if !c.cmRoots[key0] {
		t.Fatal("epoch 0 terminal root not anchored right after finalize")
	}

	// churn far past the window cap with new current-epoch roots.
	for i := uint64(3); i < 60; i++ {
		c.cmTree.Append(node(i))
		c.recordCMAnchorLocked()
	}
	if !c.cmFinal[key0] {
		t.Fatal("epoch 0 root not in the permanent finalized set")
	}
	if !c.cmRoots[key0] {
		t.Fatal("FINDING 1 regression: finalized epoch root evicted by window churn → coins stuck")
	}
	// sanity: the window itself stayed bounded.
	if len(c.cmRootOrder) > config.MaxAnchorWindow {
		t.Fatalf("current-root window unbounded: %d > %d", len(c.cmRootOrder), config.MaxAnchorWindow)
	}
}
