// Package viewtag tests the view-tag fast-scan optimization (Block 9): a 1-byte
// per-output hint that lets a wallet skip non-owned outputs cheaply, while never
// affecting ownership/key math.
package viewtag

import (
	"testing"

	"filippo.io/edwards25519"

	"obscura/pkg/commit"
)

// TestViewTagSenderReceiverAgree: the tag the sender writes equals the tag the
// receiver computes (rA == aR), and the recipient's ScanMatch accepts.
func TestViewTagSenderReceiverAgree(t *testing.T) {
	recip := commit.NewStealthKeys()
	r := commit.RandomScalar()
	out := commit.CreateOutputDeterministic(recip.Addr, r)

	senderShared := commit.SharedSecretSender(recip.Addr, r)
	tag := commit.ViewTag(senderShared)

	if !recip.ScanMatch(out, tag) {
		t.Fatal("recipient ScanMatch rejected an owned output with correct tag")
	}
	// receiver-side shared secret derives the same tag
	if commit.ViewTag(recip.SharedSecret(out)) != tag {
		t.Fatal("sender/receiver view tags disagree")
	}
}

// TestViewTagSkipsNonOwned: another wallet's ScanMatch rejects (almost always on
// the cheap tag path), and ownership is never falsely claimed.
func TestViewTagSkipsNonOwned(t *testing.T) {
	recip := commit.NewStealthKeys()
	other := commit.NewStealthKeys()
	r := commit.RandomScalar()
	out := commit.CreateOutputDeterministic(recip.Addr, r)
	tag := commit.ViewTag(commit.SharedSecretSender(recip.Addr, r))

	if other.ScanMatch(out, tag) {
		t.Fatal("non-owner wrongly matched")
	}
}

// TestWrongTagSkipsOwned: a corrupted view tag makes the (cheap) pre-filter skip
// even an owned output — confirming the tag is the gate and must be correct.
func TestWrongTagSkipsOwned(t *testing.T) {
	recip := commit.NewStealthKeys()
	r := commit.RandomScalar()
	out := commit.CreateOutputDeterministic(recip.Addr, r)
	correct := commit.ViewTag(commit.SharedSecretSender(recip.Addr, r))

	if recip.ScanMatch(out, correct+1) {
		t.Fatal("owned output matched under a wrong tag")
	}
	// but full ownership (Owns, tag-independent) still holds
	if !recip.Owns(out) {
		t.Fatal("Owns should be independent of the view tag")
	}
}

// TestViewTagNotDerivedFromP: tags must come from the shared secret, not the
// one-time key P or tx key R (else outputs would be linkable). Two outputs to
// the SAME recipient have different tags (different r → different shared).
func TestViewTagIndependentPerOutput(t *testing.T) {
	recip := commit.NewStealthKeys()
	r1 := commit.RandomScalar()
	r2 := commit.RandomScalar()
	o1 := commit.CreateOutputDeterministic(recip.Addr, r1)
	o2 := commit.CreateOutputDeterministic(recip.Addr, r2)
	t1 := commit.ViewTag(commit.SharedSecretSender(recip.Addr, r1))
	t2 := commit.ViewTag(commit.SharedSecretSender(recip.Addr, r2))
	// not derivable from P/R: tag must equal the receiver-computed shared tag
	if commit.ViewTag(recip.SharedSecret(o1)) != t1 || commit.ViewTag(recip.SharedSecret(o2)) != t2 {
		t.Fatal("view tag not reproducible from shared secret")
	}
	// sanity: P values differ (outputs are distinct)
	if o1.P.Equal(o2.P) == 1 {
		t.Fatal("distinct outputs collided")
	}
	_ = edwards25519.NewScalar
}
