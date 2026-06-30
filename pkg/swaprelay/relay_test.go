package swaprelay

import (
	"context"
	"encoding/hex"
	"strings"
	"sync"
	"testing"
	"time"

	"obscura/pkg/swapnet"
)

// stubInner records p2p (non-browser) sends.
type stubInner struct {
	mu   sync.Mutex
	sent []string // peers it received sends for
}

func (s *stubInner) Send(peer string, env *swapnet.Envelope) error {
	s.mu.Lock()
	s.sent = append(s.sent, peer)
	s.mu.Unlock()
	return nil
}

// stubCoord records the (fromPeer, kind) of injected envelopes and can echo a
// maker reply back through the relay (simulating the coordinator's maker side).
type stubCoord struct {
	relay    *Relay
	mu       sync.Mutex
	injected []string // "<peer>:<kind>"
}

func (c *stubCoord) Deliver(fromPeer string, raw []byte) {
	env, err := swapnet.ParseEnvelope(raw)
	if err != nil {
		return
	}
	c.mu.Lock()
	c.injected = append(c.injected, fromPeer)
	c.mu.Unlock()
	// On an Init, reply like a maker would: send MakerCommit back to the same peer.
	if env.Kind == swapnet.KindInit {
		reply := &swapnet.Envelope{SwapID: env.SwapID, Kind: swapnet.KindMakerCommit, Payload: []byte("commit")}
		_ = c.relay.Send(fromPeer, reply)
	}
}

func swapIDHex(b byte) string {
	id := make([]byte, 32)
	for i := range id {
		id[i] = b
	}
	return hex.EncodeToString(id)
}

func TestRelayBridgesBrowserToMakerAndBack(t *testing.T) {
	inner := &stubInner{}
	r := New(inner)
	coord := &stubCoord{relay: r}
	r.SetCoordinator(coord)

	sid := swapIDHex(0xAB)

	// Submit before open must fail (no inbox for maker replies).
	if err := r.Submit(sid, byte(swapnet.KindInit), []byte("init")); err == nil {
		t.Fatal("Submit before Open should fail")
	}

	if err := r.Open(sid); err != nil {
		t.Fatal(err)
	}
	// browser sends Init -> coordinator injects -> maker replies MakerCommit -> inbox
	if err := r.Submit(sid, byte(swapnet.KindInit), []byte("init")); err != nil {
		t.Fatal(err)
	}

	// the coordinator saw an Init from the synthetic browser peer
	coord.mu.Lock()
	gotPeer := coord.injected[0]
	coord.mu.Unlock()
	if gotPeer != BrowserPrefix+sid {
		t.Fatalf("injected fromPeer = %q, want %q", gotPeer, BrowserPrefix+sid)
	}

	// the browser long-polls and receives the maker's MakerCommit
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	kind, payload, ok := r.Recv(ctx, sid)
	if !ok || kind != byte(swapnet.KindMakerCommit) || string(payload) != "commit" {
		t.Fatalf("Recv = (%d,%q,%v), want MakerCommit/commit", kind, payload, ok)
	}

	// no non-browser p2p send happened
	if len(inner.sent) != 0 {
		t.Fatalf("inner transport should not have been used, got %v", inner.sent)
	}
}

func TestRelayNonBrowserFallsThrough(t *testing.T) {
	inner := &stubInner{}
	r := New(inner)
	env := &swapnet.Envelope{Kind: swapnet.KindInit, Payload: []byte("x")}
	if err := r.Send("203.0.113.7:18080", env); err != nil {
		t.Fatal(err)
	}
	if len(inner.sent) != 1 || !strings.Contains(inner.sent[0], "203.0.113.7") {
		t.Fatalf("expected inner transport send to the p2p peer, got %v", inner.sent)
	}
}

func TestRelayRecvTimeoutAndUnknown(t *testing.T) {
	r := New(&stubInner{})
	// unknown session
	ctx := context.Background()
	if _, _, ok := r.Recv(ctx, swapIDHex(0x01)); ok {
		t.Fatal("Recv on unknown session should be ok=false")
	}
	// open then time out with no messages
	sid := swapIDHex(0x02)
	_ = r.Open(sid)
	tctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, _, ok := r.Recv(tctx, sid); ok {
		t.Fatal("Recv should time out to ok=false when no message arrives")
	}
}

func TestOpenRejectsBadSwapID(t *testing.T) {
	r := New(&stubInner{})
	if err := r.Open("tooshort"); err == nil {
		t.Fatal("Open should reject a bad swap id")
	}
}
