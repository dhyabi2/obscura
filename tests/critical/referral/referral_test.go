// Package referral verifies the sybil-resistance invariant for the referral /
// viral-loop design (Block 5): referrals are NEVER minted, so a self-referrer
// gains nothing and supply cannot be inflated. See docs/INVENTION_REFERRAL.md.
package referral

import (
	"testing"

	"obscura/pkg/config"
	"obscura/tests/critical/harness"
)

// TestReferralAddsNothingToMint: a referrer tag does not change the coinbase
// mint — so self-referral (or any referral) yields zero extra coins.
func TestReferralAddsNothingToMint(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	w := harness.NewWallet("miner")
	referrer := harness.NewWallet("referrer").AddressBytes()

	for _, fees := range []uint64{0, 1_000_000_000, 5_000_000_000} {
		withRef := c.ExpectedCoinbaseMinted(fees, referrer)
		without := c.ExpectedCoinbaseMinted(fees, nil)
		alsoSelf := c.ExpectedCoinbaseMinted(fees, w.AddressBytes()) // self-referral
		if withRef != without || alsoSelf != without {
			t.Fatalf("referral changed mint (fees=%d): none=%d ref=%d self=%d",
				fees, without, withRef, alsoSelf)
		}
	}
}

// TestEmissionIsExactlyBaseReward: over many blocks (mined with referrer tags),
// total emitted supply equals exactly Σ BlockReward — referrals never inflate.
func TestEmissionIsExactlyBaseReward(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	w := harness.NewWallet("miner2")

	const n = 5
	harness.MineN(t, c, w, n) // coinbases here mint base reward only

	var expect, e uint64
	for i := 0; i < n; i++ {
		r := config.BlockReward(e)
		expect += r
		e += r
	}
	if c.Emitted() != expect {
		t.Fatalf("emitted=%d, want Σ BlockReward=%d (referral must not inflate)", c.Emitted(), expect)
	}
}
