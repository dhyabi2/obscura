package config

// Confidential staking-vault parameters. A vault locks OBX for a Term (in blocks)
// and pays yield from the (already-emitted, supply-capped) incentive pool — no new
// inflation. See docs/INVENTION_VAULTS.md.
//
// Terms/rates are vars (not consts) so a devnet/test can use short terms.
// Block time ≈ 120 s ⇒ ≈ 720 blocks/day. Defaults: 30d=1%, 90d=4%, 365d=20%.
var (
	VaultTerms    = []uint64{21_600, 64_800, 262_800}
	VaultRatesBps = []uint64{100, 400, 2000}
)

// VaultRateBps returns the yield rate (basis points) for an allowed term, and
// whether the term is allowed.
func VaultRateBps(term uint64) (uint64, bool) {
	for i, t := range VaultTerms {
		if t == term {
			return VaultRatesBps[i], true
		}
	}
	return 0, false
}
