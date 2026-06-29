// Package swapd is the cross-chain swap daemon for XMR↔Obscura atomic swaps
// (Block 16 — see docs/INVENTION_SWAPS.md). The Obscura leg is driven by the
// real chain/wallet/swap primitives; the Monero leg is abstracted behind
// MoneroClient so a production build plugs in a monero-wallet-rpc adapter, while
// tests use the in-memory MockMonero to exercise the full two-leg flow.
package swapd

import (
	"errors"
	"fmt"
	"sync"

	"filippo.io/edwards25519"
)

// MoneroClient is the minimal Monero-side capability the swap needs. A real
// implementation talks to monero-wallet-rpc / monerod; it never changes Obscura
// consensus.
type MoneroClient interface {
	// Lock sends `amount` (atomic units) to a one-time spend public key
	// spendPub = (s_a + s_b)·G, returning a lock id. The funds are spendable
	// only by whoever knows the scalar s_a + s_b.
	Lock(amount uint64, spendPub []byte) (lockID string, err error)
	// Confirmed reports whether the lock has enough confirmations to be safe
	// against reorgs (production: ≥ N confs and ≥ 2× expected reorg window).
	Confirmed(lockID string) bool
	// Sweep spends a locked output to `dest`, proving knowledge of the spend
	// secret (s_a + s_b). Fails if the secret is wrong or already spent.
	Sweep(lockID string, spendSecret *edwards25519.Scalar, dest string) error
	// Balance returns the swept balance credited to a destination (for tests).
	Balance(dest string) uint64
}

// XMRSpendPub returns the Monero spend public key the XMR is locked to:
// S_a + S_b (sum of the two parties' shares), on ed25519 — no cross-curve DLEQ
// because Obscura and Monero share the curve.
func XMRSpendPub(Sa, Sb []byte) ([]byte, error) {
	A, err := new(edwards25519.Point).SetBytes(Sa)
	if err != nil {
		return nil, err
	}
	B, err := new(edwards25519.Point).SetBytes(Sb)
	if err != nil {
		return nil, err
	}
	return new(edwards25519.Point).Add(A, B).Bytes(), nil
}

// MockMonero is an in-memory Monero stand-in for tests: a ledger of locks and
// destination balances. It enforces the real spend rule — sweeping requires the
// scalar whose point equals the lock's spend pubkey.
type MockMonero struct {
	mu    sync.Mutex
	seq   int
	locks map[string]*mLock
	bal   map[string]uint64
}

type mLock struct {
	amount uint64
	pub    []byte
	spent  bool
}

// NewMockMonero creates an empty mock Monero ledger.
func NewMockMonero() *MockMonero {
	return &MockMonero{locks: make(map[string]*mLock), bal: make(map[string]uint64)}
}

func (m *MockMonero) Lock(amount uint64, spendPub []byte) (string, error) {
	if len(spendPub) != 32 {
		return "", errors.New("swapd: bad spend pubkey")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	id := fmt.Sprintf("xmrlock-%d", m.seq)
	m.locks[id] = &mLock{amount: amount, pub: append([]byte(nil), spendPub...)}
	return id, nil
}

func (m *MockMonero) Confirmed(lockID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.locks[lockID]
	return ok // mock: confirmed as soon as it exists
}

func (m *MockMonero) Sweep(lockID string, spendSecret *edwards25519.Scalar, dest string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.locks[lockID]
	if !ok {
		return errors.New("swapd: unknown lock")
	}
	if l.spent {
		return errors.New("swapd: lock already spent")
	}
	// the real Monero rule: you must know s with s·G == the lock's spend pubkey
	got := new(edwards25519.Point).ScalarBaseMult(spendSecret).Bytes()
	if string(got) != string(l.pub) {
		return errors.New("swapd: wrong spend key — cannot sweep")
	}
	l.spent = true
	m.bal[dest] += l.amount
	return nil
}

func (m *MockMonero) Balance(dest string) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bal[dest]
}

var _ MoneroClient = (*MockMonero)(nil)
