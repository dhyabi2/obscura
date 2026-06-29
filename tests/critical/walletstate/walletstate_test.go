// Package walletstate tests wallet persistence + incremental scanning (Block 18):
// state survives a reload and is usable for spending, and a restored wallet
// scans only NEW blocks instead of rescanning from genesis.
package walletstate

import (
	"bytes"
	"testing"

	"obscura/pkg/config"
	"obscura/pkg/keystore"
	"obscura/tests/critical/harness"
)

// keep the KDF cheap so these tests stay fast; production defaults are memory-hard.
func init() {
	keystore.DefaultTime = 1
	keystore.DefaultMemKiB = 1024
	keystore.DefaultThreads = 1
}

// TestEncryptedStateRoundTrip mirrors the CLI's encrypted .state file: the scan
// state (which holds output amounts AND one-time secret keys) is encrypted at
// rest with the wallet passphrase, and decrypts back to a usable wallet.
func TestEncryptedStateRoundTrip(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("enc-alice")
	harness.Funded(t, c, alice, 4)
	bal := alice.Balance()
	if bal == 0 {
		t.Fatal("no balance")
	}

	plain := alice.MarshalState()
	pass := []byte("state-passphrase")
	blob, err := keystore.Encrypt(plain, pass)
	if err != nil {
		t.Fatalf("encrypt state: %v", err)
	}
	if !keystore.IsEncrypted(blob) {
		t.Fatal("encrypted state not recognized as encrypted")
	}
	if bytes.Equal(blob, plain) || bytes.Contains(blob, plain) {
		t.Fatal("plaintext state present in the encrypted blob (balances/keys leaked)")
	}

	// wrong passphrase must not open the state
	if _, err := keystore.Decrypt(blob, []byte("nope")); err == nil {
		t.Fatal("encrypted state opened with the wrong passphrase")
	}

	got, err := keystore.Decrypt(blob, pass)
	if err != nil {
		t.Fatalf("decrypt state: %v", err)
	}
	restored := harness.NewWallet("enc-alice")
	if err := restored.RestoreState(got); err != nil {
		t.Fatalf("restore decrypted state: %v", err)
	}
	if restored.Balance() != bal {
		t.Fatalf("restored balance %d != %d", restored.Balance(), bal)
	}
	if restored.LastScanned() != alice.LastScanned() {
		t.Fatalf("restored lastScanned %d != %d", restored.LastScanned(), alice.LastScanned())
	}
}

func TestStateRoundTripAndSpend(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("ws-alice")
	harness.Funded(t, c, alice, 4)
	bal := alice.Balance()
	if bal == 0 {
		t.Fatal("no balance")
	}
	last := alice.LastScanned()
	if last != c.Height() {
		t.Fatalf("lastScanned=%d, want %d", last, c.Height())
	}

	// persist → reload into a fresh wallet from the same seed
	state := alice.MarshalState()
	alice2 := harness.NewWallet("ws-alice")
	if err := alice2.RestoreState(state); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if alice2.Balance() != bal {
		t.Fatalf("restored balance %d != %d", alice2.Balance(), bal)
	}
	if alice2.LastScanned() != last {
		t.Fatalf("restored lastScanned %d != %d", alice2.LastScanned(), last)
	}

	// the restored wallet can spend WITHOUT rescanning from genesis
	bob := harness.NewWallet("ws-bob")
	txn, err := alice2.CreateTransaction(c, bob.Address(), bal/4, 1_000_000_000)
	if err != nil {
		t.Fatalf("spend from restored state: %v", err)
	}
	if err := c.ValidateStandaloneTx(txn); err != nil {
		t.Fatalf("restored-wallet tx invalid: %v", err)
	}
}

func TestIncrementalScanOnlyNewBlocks(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("ws2-alice")

	// scan up to height H1
	harness.MineN(t, c, alice, 3)
	harness.ScanAll(c, alice)
	h1 := alice.LastScanned()
	bal1 := alice.Balance()

	// mine more, then incrementally scan ONLY the new blocks
	harness.MineN(t, c, alice, 3)
	for h := alice.LastScanned() + 1; h <= c.Height(); h++ {
		b, ok := c.BlockByHeight(h)
		if !ok {
			t.Fatalf("missing block %d", h)
		}
		alice.ScanBlock(b)
	}
	if alice.LastScanned() != c.Height() || alice.LastScanned() <= h1 {
		t.Fatalf("incremental scan did not advance: last=%d h1=%d tip=%d", alice.LastScanned(), h1, c.Height())
	}
	if alice.Balance() <= bal1 {
		t.Fatalf("balance did not grow after scanning new blocks: %d <= %d", alice.Balance(), bal1)
	}
	_ = config.Ticker
}
