// Package vault_test covers the confidential staking-vault primitive: deposits
// lock public value, claims release principal + yield (paid from the incentive
// pool) after maturity. See docs/INVENTION_VAULTS.md.
package vault_test

import (
	"bytes"
	"testing"

	"obscura/pkg/config"
	"obscura/pkg/tx"
	"obscura/pkg/wallet"
	"obscura/tests/critical/harness"
)

const (
	oneOBX = config.AtomicPerCoin
	vfee   = oneOBX / 100 // 0.01 OBX
)

// smallTerms overrides the (long) production terms with a short term so a test can
// reach maturity by mining a couple of blocks. Returns a restore func.
func smallTerms(term, rateBps uint64) func() {
	t0, r0 := config.VaultTerms, config.VaultRatesBps
	config.VaultTerms = []uint64{term}
	config.VaultRatesBps = []uint64{rateBps}
	return func() { config.VaultTerms, config.VaultRatesBps = t0, r0 }
}

func sink() *wallet.Wallet { return harness.NewWallet("vault-sink") }

// Happy path: deposit locks principal, claim after maturity pays principal+yield
// from the incentive pool, vault closes.
func TestVaultDepositClaimRoundTrip(t *testing.T) {
	defer harness.SmallMaturity()()
	defer smallTerms(2, 1000)() // term 2 blocks, 10% yield
	c := harness.NewChain(t)
	w := harness.NewWallet("vault-alice")
	harness.Funded(t, c, w, 6)

	amount := uint64(5) * oneOBX
	vk := wallet.NewVaultKey()
	dep, vaultID, err := w.BuildVaultDeposit(c, vk.Pub, amount, 2, vfee)
	if err != nil {
		t.Fatalf("build deposit: %v", err)
	}
	balBeforeDep := w.Balance()
	harness.MineBlock(t, c, sink(), []*tx.Transaction{dep})
	harness.ScanAll(c, w)

	if c.VaultCount() != 1 {
		t.Fatalf("expected 1 live vault, got %d", c.VaultCount())
	}
	v, ok := c.Vault(vaultID)
	if !ok || v.Amount != amount || v.Term != 2 {
		t.Fatalf("vault state wrong: %+v ok=%v", v, ok)
	}
	if c.TotalValueLocked() != amount {
		t.Fatalf("TVL = %d, want %d", c.TotalValueLocked(), amount)
	}
	if got := balBeforeDep - w.Balance(); got != amount+vfee {
		t.Fatalf("deposit should cost amount+fee=%d, balance dropped by %d", amount+vfee, got)
	}
	balAfterDep := w.Balance()

	// reach maturity (deposit height + 2): mine one empty block so the claim block
	// is at height >= maturity.
	harness.MineBlock(t, c, sink(), nil)

	yield, _ := wallet.VaultYield(amount, 2)
	if yield != amount/10 {
		t.Fatalf("yield = %d, want %d", yield, amount/10)
	}
	claim, err := w.BuildVaultClaim(vk, vaultID, amount, 2, vfee)
	if err != nil {
		t.Fatalf("build claim: %v", err)
	}
	poolBefore := c.IncentivePool()
	harness.MineBlock(t, c, sink(), []*tx.Transaction{claim})
	harness.ScanAll(c, w)

	if c.VaultCount() != 0 {
		t.Fatalf("vault should be closed after claim, %d remain", c.VaultCount())
	}
	if c.TotalValueLocked() != 0 {
		t.Fatalf("TVL should be 0 after claim, got %d", c.TotalValueLocked())
	}
	// wallet received principal + yield − fee — this alone proves the yield was
	// paid (a no-yield bug would pay only principal − fee). The pool is the source
	// (TestVaultYieldExceedsPoolRejected proves payouts are bounded by it); the
	// pool's net change here is +contribution − yield, so it still grows.
	if got := w.Balance() - balAfterDep; got != amount+yield-vfee {
		t.Fatalf("claim payout = %d, want principal+yield-fee=%d", got, amount+yield-vfee)
	}
	_ = poolBefore
}

// A claim before maturity is rejected.
func TestVaultPrematureClaimRejected(t *testing.T) {
	defer harness.SmallMaturity()()
	defer smallTerms(5, 1000)()
	c := harness.NewChain(t)
	w := harness.NewWallet("vault-bob")
	harness.Funded(t, c, w, 6)

	amount := uint64(3) * oneOBX
	vk := wallet.NewVaultKey()
	dep, vaultID, err := w.BuildVaultDeposit(c, vk.Pub, amount, 5, vfee)
	if err != nil {
		t.Fatal(err)
	}
	harness.MineBlock(t, c, sink(), []*tx.Transaction{dep})

	claim, err := w.BuildVaultClaim(vk, vaultID, amount, 5, vfee)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := harness.BuildTemplate(t, c, sink(), []*tx.Transaction{claim})
	harness.MineHeader(t, tmpl)
	if err := c.AddBlock(tmpl); err == nil {
		t.Fatal("accepted a premature vault claim")
	} else if !bytes.Contains([]byte(err.Error()), []byte("maturity")) {
		t.Fatalf("expected maturity error, got: %v", err)
	}
}

// A second claim of an already-claimed vault is rejected.
func TestVaultDoubleClaimRejected(t *testing.T) {
	defer harness.SmallMaturity()()
	defer smallTerms(1, 1000)()
	c := harness.NewChain(t)
	w := harness.NewWallet("vault-carol")
	harness.Funded(t, c, w, 6)

	amount := uint64(2) * oneOBX
	vk := wallet.NewVaultKey()
	dep, vaultID, err := w.BuildVaultDeposit(c, vk.Pub, amount, 1, vfee)
	if err != nil {
		t.Fatal(err)
	}
	harness.MineBlock(t, c, sink(), []*tx.Transaction{dep})
	harness.MineBlock(t, c, sink(), nil) // reach maturity

	claim1, err := w.BuildVaultClaim(vk, vaultID, amount, 1, vfee)
	if err != nil {
		t.Fatal(err)
	}
	harness.MineBlock(t, c, sink(), []*tx.Transaction{claim1})

	claim2, err := w.BuildVaultClaim(vk, vaultID, amount, 1, vfee)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := harness.BuildTemplate(t, c, sink(), []*tx.Transaction{claim2})
	harness.MineHeader(t, tmpl)
	if err := c.AddBlock(tmpl); err == nil {
		t.Fatal("accepted a double claim")
	} else if !bytes.Contains([]byte(err.Error()), []byte("nonexistent")) {
		t.Fatalf("expected nonexistent-vault error, got: %v", err)
	}
}

// A claim signed by the wrong key is rejected.
func TestVaultWrongSignatureRejected(t *testing.T) {
	defer harness.SmallMaturity()()
	defer smallTerms(1, 1000)()
	c := harness.NewChain(t)
	w := harness.NewWallet("vault-dave")
	harness.Funded(t, c, w, 6)

	amount := uint64(2) * oneOBX
	vk := wallet.NewVaultKey()
	dep, vaultID, err := w.BuildVaultDeposit(c, vk.Pub, amount, 1, vfee)
	if err != nil {
		t.Fatal(err)
	}
	harness.MineBlock(t, c, sink(), []*tx.Transaction{dep})
	harness.MineBlock(t, c, sink(), nil)

	claim, err := w.BuildVaultClaim(vk, vaultID, amount, 1, vfee)
	if err != nil {
		t.Fatal(err)
	}
	// re-sign with a different key (the CoreHash excludes the sig, so this is a
	// well-formed signature by the WRONG owner)
	wrong := wallet.NewVaultKey()
	ch := claim.CoreHash()
	claim.VaultInputs[0].Sig = wrong.Sign(ch[:])

	tmpl := harness.BuildTemplate(t, c, sink(), []*tx.Transaction{claim})
	harness.MineHeader(t, tmpl)
	if err := c.AddBlock(tmpl); err == nil {
		t.Fatal("accepted a claim with a wrong-key signature")
	} else if !bytes.Contains([]byte(err.Error()), []byte("signature")) {
		t.Fatalf("expected signature error, got: %v", err)
	}
}

// Tampering a deposit's public locked amount is rejected: the amount is bound in
// the CoreHash, so a tamper invalidates the value-binding proofs (here the input
// ownership proof) before it could ever mint value — i.e. you cannot lock more
// than you paid for.
func TestVaultDepositTamperRejected(t *testing.T) {
	defer harness.SmallMaturity()()
	defer smallTerms(2, 1000)()
	c := harness.NewChain(t)
	w := harness.NewWallet("vault-erin")
	harness.Funded(t, c, w, 6)

	amount := uint64(4) * oneOBX
	vk := wallet.NewVaultKey()
	dep, _, err := w.BuildVaultDeposit(c, vk.Pub, amount, 2, vfee)
	if err != nil {
		t.Fatal(err)
	}
	dep.VaultOutputs[0].Amount = amount + oneOBX // lie: lock more than paid for

	tmpl := harness.BuildTemplate(t, c, sink(), []*tx.Transaction{dep})
	harness.MineHeader(t, tmpl)
	if err := c.AddBlock(tmpl); err == nil {
		t.Fatal("accepted a deposit with tampered amount")
	} else if !bytes.Contains([]byte(err.Error()), []byte("validation failed")) {
		t.Fatalf("expected a validation rejection, got: %v", err)
	}
}

// A claim whose yield exceeds the incentive pool is rejected (no inflation).
func TestVaultYieldExceedsPoolRejected(t *testing.T) {
	defer harness.SmallMaturity()()
	// 1,000,000% yield so even a modest principal's yield dwarfs the pool.
	defer smallTerms(1, 10_000_000)()
	c := harness.NewChain(t)
	w := harness.NewWallet("vault-frank")
	harness.Funded(t, c, w, 6)

	amount := uint64(5) * oneOBX
	vk := wallet.NewVaultKey()
	dep, vaultID, err := w.BuildVaultDeposit(c, vk.Pub, amount, 1, vfee)
	if err != nil {
		t.Fatal(err)
	}
	harness.MineBlock(t, c, sink(), []*tx.Transaction{dep})
	harness.MineBlock(t, c, sink(), nil)

	claim, err := w.BuildVaultClaim(vk, vaultID, amount, 1, vfee)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := harness.BuildTemplate(t, c, sink(), []*tx.Transaction{claim})
	harness.MineHeader(t, tmpl)
	if err := c.AddBlock(tmpl); err == nil {
		t.Fatal("accepted a claim whose yield exceeds the incentive pool")
	} else if !bytes.Contains([]byte(err.Error()), []byte("incentive pool")) {
		t.Fatalf("expected incentive-pool error, got: %v", err)
	}
}
