package p2p

import (
	"testing"
	"time"
)

// TestRateExemptConsensusMessages locks in the invariant that block-propagation
// and chain-sync messages are NEVER subject to the inbound token bucket. Dropping
// these silently stalls sync and can prevent the network from converging on one
// chain, so they must stay exempt even though every other message type is bounded.
func TestRateExemptConsensusMessages(t *testing.T) {
	exempt := map[byte]bool{
		msgBlock:  true,
		msgTip:    true,
		msgGetBlk: true,
		msgGetTip: true,
	}
	for typ := byte(1); typ <= 11; typ++ {
		if got, want := rateExempt(typ), exempt[typ]; got != want {
			t.Errorf("rateExempt(%d) = %v, want %v", typ, got, want)
		}
	}
}

// TestRateBucketLimitsNonExemptFlood verifies the bucket still throttles a flood
// of non-consensus messages: after draining its burst capacity a peer is denied.
func TestRateBucketLimitsNonExemptFlood(t *testing.T) {
	n := &Node{}
	p := &peer{tokens: rateBucketCap, lastFill: time.Now()}
	allowed := 0
	for i := 0; i < int(rateBucketCap)+50; i++ {
		if n.allowMsg(p) {
			allowed++
		}
	}
	if allowed > int(rateBucketCap) {
		t.Fatalf("bucket allowed %d msgs, want <= burst cap %.0f", allowed, rateBucketCap)
	}
	if allowed == 0 {
		t.Fatal("bucket allowed 0 msgs, expected at least the burst capacity")
	}
}
