package chain

import (
	"obscura/pkg/config"
)

// Incentive accounting: the three pillars (mining reward, holding-pool funding,
// referral viral-loop bonus). All deterministic so every node agrees.

// blockEconomics computes, for a block at the current emitted supply with the
// given collected fees and (optional) referrer tag, the components of the
// coinbase. Returns:
//   minted          = coinbase output total (what the miner receives)
//   poolContribution = amount added to the holding-incentive pool
//   referralBonus    = freshly-minted bonus paid to the referrer (0 if none)
//   newEmission      = total new coins created (base reward + referral bonus)
func (c *Chain) blockEconomics(referrerTag []byte) (baseReward, poolContribution, referralBonus uint64) {
	baseReward = config.BlockReward(c.emitted)
	poolContribution = baseReward * config.IncentivePoolBps / 10_000
	referralBonus = c.referralBonusFor(referrerTag, baseReward)
	return
}

// referralBonusFor computes the capped, decaying referral bonus for a referrer.
// The bonus starts at ReferralMaxBps of the base reward and decays linearly to
// zero across ReferralMaxClaims successful referrals, after which it is zero.
// This rewards early sharing while bounding total inflation and limiting the
// payoff of sybil self-referral.
func (c *Chain) referralBonusFor(referrerTag []byte, baseReward uint64) uint64 {
	if len(referrerTag) == 0 {
		return 0
	}
	used := c.referral[hexstr(referrerTag)]
	if used >= config.ReferralMaxClaims {
		return 0
	}
	maxBonus := baseReward * config.ReferralMaxBps / 10_000
	// linear decay: remaining fraction = (MaxClaims - used) / MaxClaims
	remaining := config.ReferralMaxClaims - used
	return maxBonus * remaining / config.ReferralMaxClaims
}

// ExpectedCoinbaseMinted returns the coinbase output total the chain expects for
// a block built on the current tip with the given fees and referrer tag. It
// takes the read lock and uses overflow-checked arithmetic.
func (c *Chain) ExpectedCoinbaseMinted(fees uint64, referrerTag []byte) uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, err := c.expectedCoinbaseMintedChecked(fees, referrerTag)
	if err != nil {
		return 0
	}
	return v
}

// ReferralBonus exposes the referral bonus calculation for builders.
func (c *Chain) ReferralBonus(referrerTag []byte) uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	base := config.BlockReward(c.emitted)
	return c.referralBonusFor(referrerTag, base)
}
