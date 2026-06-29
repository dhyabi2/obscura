package chain

import (
	"math/big"
)

// VaultEntry is a live staking-vault deposit. Yield is paid from the incentive
// pool at claim time (docs/INVENTION_VAULTS.md). The rate is SNAPSHOTTED at
// deposit (RateBps) so a later change to the term table can never strand a
// depositor or alter their promised yield. Rolled back on reorg, rebuilt on
// replay — same discipline as swaps.
type VaultEntry struct {
	Amount        uint64
	Term          uint64
	RateBps       uint64 // yield rate locked in at deposit time
	OwnerKey      []byte
	DepositHeight uint64
}

// Maturity is the first height at which the vault may be claimed.
func (e *VaultEntry) Maturity() uint64 { return e.DepositHeight + e.Term }

// vaultYield computes Amount·rateBps/10_000 with no uint64 overflow (Amount can
// approach the money supply). ok=false only on (impossible-in-practice) overflow.
func vaultYield(amount, rateBps uint64) (uint64, bool) {
	y := new(big.Int).Mul(new(big.Int).SetUint64(amount), new(big.Int).SetUint64(rateBps))
	y.Div(y, big.NewInt(10_000))
	if !y.IsUint64() {
		return 0, false
	}
	return y.Uint64(), true
}

// --- read-only queries (RPC / explorer / tests) ---

// Vault returns a copy of a live vault by key.
func (c *Chain) Vault(key []byte) (VaultEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.vaults[hexstr(key)]
	if !ok {
		return VaultEntry{}, false
	}
	return *e, true
}

// VaultCount returns the number of live vaults.
func (c *Chain) VaultCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.vaults)
}

// VaultInfo is a public, explorer-facing view of a live vault.
type VaultInfo struct {
	Amount        uint64
	Term          uint64
	RateBps       uint64
	DepositHeight uint64
	Maturity      uint64
}

// VaultList returns a snapshot of all live vaults (order not guaranteed).
func (c *Chain) VaultList() []VaultInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]VaultInfo, 0, len(c.vaults))
	for _, e := range c.vaults {
		out = append(out, VaultInfo{
			Amount: e.Amount, Term: e.Term, RateBps: e.RateBps,
			DepositHeight: e.DepositHeight, Maturity: e.Maturity(),
		})
	}
	return out
}

// TotalValueLocked returns the sum of all live vault principals (the supply sink).
func (c *Chain) TotalValueLocked() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var tvl uint64
	for _, e := range c.vaults {
		tvl += e.Amount // bounded by total emitted supply; no realistic overflow
	}
	return tvl
}
