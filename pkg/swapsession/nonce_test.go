package swapsession

import (
	"bytes"
	"math/big"
	"path/filepath"
	"testing"

	"obscura/pkg/commit"
)

// (d) deterministic-nonce helper: stable for the same (secret, coreHash) and
// distinct for a different coreHash (a renegotiated swap) or a different secret.
func TestDeriveNonceDeterministic(t *testing.T) {
	secret := commit.RandomScalar()
	other := commit.RandomScalar()
	core1 := []byte("core-hash-A")
	core2 := []byte("core-hash-B")

	n1 := DeriveNonce(secret, core1)
	n1again := DeriveNonce(secret, core1)
	n2 := DeriveNonce(secret, core2)
	nOther := DeriveNonce(other, core1)

	// STABLE: same inputs -> identical nonce (no nonce churn on retry).
	if !bytes.Equal(n1.Bytes(), n1again.Bytes()) {
		t.Fatal("DeriveNonce not deterministic for identical inputs")
	}
	// DISTINCT per coreHash: a different signing context yields an independent
	// nonce, so a retry over different terms can never reuse a nonce (#13).
	if bytes.Equal(n1.Bytes(), n2.Bytes()) {
		t.Fatal("DeriveNonce collides across different coreHashes — nonce reuse risk")
	}
	// DISTINCT per secret: another party's nonce differs even for the same context.
	if bytes.Equal(n1.Bytes(), nOther.Bytes()) {
		t.Fatal("DeriveNonce collides across different secrets")
	}
	// never the zero scalar (a zero nonce would expose the secret directly).
	zero := make([]byte, 32)
	for _, n := range []*[32]byte{ptrBytes(n1), ptrBytes(n2), ptrBytes(nOther)} {
		if bytes.Equal(n[:], zero) {
			t.Fatal("DeriveNonce produced the zero scalar")
		}
	}
}

func ptrBytes(s interface{ Bytes() []byte }) *[32]byte {
	var b [32]byte
	copy(b[:], s.Bytes())
	return &b
}

// ownTermHash must be stable per (swapID, own shares, amounts) and change when any
// term changes — this is what guarantees the per-party nonce uniqueness.
func TestOwnTermHashSensitivity(t *testing.T) {
	id := swapID(0x44)
	a := commit.RandomScalar().Bytes()
	sA := commit.RandomScalar().Bytes()

	base := ownTermHash(id, a, sA, 100, big.NewInt(200))
	if !bytes.Equal(base, ownTermHash(id, a, sA, 100, big.NewInt(200))) {
		t.Fatal("ownTermHash not stable")
	}
	if bytes.Equal(base, ownTermHash(id, a, sA, 101, big.NewInt(200))) {
		t.Fatal("ownTermHash ignored OBX amount")
	}
	if bytes.Equal(base, ownTermHash(id, a, sA, 100, big.NewInt(201))) {
		t.Fatal("ownTermHash ignored XNO amount")
	}
	other := commit.RandomScalar().Bytes()
	if bytes.Equal(base, ownTermHash(id, other, sA, 100, big.NewInt(200))) {
		t.Fatal("ownTermHash ignored claim share")
	}
}

// SwapState must round-trip through JSON, preserving the (sensitive) own share
// and all the public material needed to resume or refund.
func TestSwapStatePersistence(t *testing.T) {
	h := newDevnet(t)
	id := swapID(0x55)
	maker := NewMaker(id, testOBXAmount, testXNO, testFee, "maker-xno-dest", h)
	taker := NewTaker(id, testOBXAmount, testXNO, testFee, h)
	mc, err := maker.HandleInit(taker.Init())
	if err != nil {
		t.Fatalf("HandleInit: %v", err)
	}
	if err := taker.HandleMakerCommit(mc); err != nil {
		t.Fatalf("HandleMakerCommit: %v", err)
	}

	path := filepath.Join(t.TempDir(), "maker-state.json")
	if err := maker.State().Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got.Role != RoleMaker {
		t.Fatalf("role = %s", got.Role)
	}
	if !bytes.Equal(got.OwnShareClaim, maker.State().OwnShareClaim) ||
		!bytes.Equal(got.OwnShareXNO, maker.State().OwnShareXNO) {
		t.Fatal("own shares not preserved")
	}
	if !bytes.Equal(got.ClaimKey, maker.State().ClaimKey) ||
		!bytes.Equal(got.XNOAccountPub, maker.State().XNOAccountPub) {
		t.Fatal("joint public material not preserved")
	}
	// the maker's persisted state must NOT contain the taker's private shares.
	if bytes.Equal(got.OwnShareClaim, taker.a.Bytes()) || bytes.Equal(got.OwnShareXNO, taker.sA.Bytes()) {
		t.Fatal("persisted maker state leaked the taker's private share")
	}
}
