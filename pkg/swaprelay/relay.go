// Package swaprelay lets a BROWSER taker complete a swap with a node-hosted maker
// without the node ever holding the taker's keys. It is a thin, additive bridge —
// it changes NOTHING in the swap protocol or the coordinator.
//
// How it works: the browser runs the taker (pkg/takerdrive in WASM) and exchanges
// the same swapsession envelopes it always would, but over HTTP instead of p2p.
// The relay injects those envelopes into the node's UNCHANGED swapnet.Coordinator
// as if they arrived from a peer named "browser:<swapID>", and it implements
// swapnet.Transport so the maker's replies addressed to that synthetic peer are
// queued for the browser to long-poll. To the coordinator's maker side this is
// indistinguishable from a remote p2p taker; every swapsession guard still runs.
// An anonymous web caller CANNOT touch the operator's funds (the relay holds no
// keys; the browser signs its own legs). BUT the relay does NOT make the browser
// swap trustless: a browser taker is a LIGHT CLIENT whose only view of the OBX
// chain is this relay, so a malicious operator can fabricate chain state and steal
// the taker's locked XNO (audit C-1, see pkg/takerdrive doc). The relay can DENY
// service always, and can STEAL a browser taker's XNO unless the taker uses its
// own node — not the "deny-only" guarantee a full-node p2p taker enjoys.
//
// Non-browser peers fall through to the wrapped inner Transport (real p2p), so a
// node keeps serving ordinary node-to-node swaps unchanged.
package swaprelay

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"obscura/pkg/swapnet"
)

// BrowserPrefix tags the synthetic peer handle a browser taker is bound to. The
// session key is the hex SwapID following the prefix.
const BrowserPrefix = "browser:"

// inboxCap bounds queued maker→browser messages per swap. A swap has at most a
// handful in flight (MakerCommit, Funded, ClaimPreSig); 16 is generous.
const inboxCap = 16

// AUDIT H-1/H-3: the relay endpoints are PUBLIC, so the session map must be
// bounded or `/swaps/relay/open` is an unauthenticated memory-exhaustion vector
// (and each browser swap also spawns a coordinator maker session, so an unbounded
// relay also defeats the per-peer swap cap). We cap concurrent sessions and evict
// idle ones: a real swap completes in minutes, so a generous idle TTL reclaims
// abandoned/leaked sessions without a Close call from the (untrusted) client.
const (
	maxSessions = 1024            // hard ceiling on concurrent browser swap sessions
	sessionTTL  = 15 * time.Minute // idle eviction window (last touch)
)

// deliverer is the slice of *swapnet.Coordinator the relay needs: inject an
// inbound envelope. Kept as an interface so the relay can be unit-tested without a
// full coordinator and to avoid an init-order cycle (the coordinator is built with
// the relay as its Transport, then SetCoordinator wires the back-reference).
type deliverer interface {
	Deliver(fromPeer string, raw []byte)
}

// Relay wraps an inner swapnet.Transport and bridges browser HTTP sessions.
type Relay struct {
	inner swapnet.Transport
	coord deliverer

	mu       sync.Mutex
	sessions map[string]*session
}

type msg struct {
	kind    swapnet.Kind
	payload []byte
}

type session struct {
	inbox   chan msg
	touched time.Time // last activity, for idle-TTL eviction
}

// New wraps inner (typically *swapnet.P2PTransport) as a browser-aware Transport.
func New(inner swapnet.Transport) *Relay {
	return &Relay{inner: inner, sessions: make(map[string]*session)}
}

// SetCoordinator wires the coordinator the relay injects browser envelopes into.
// Called once after the coordinator is constructed (it takes the relay as its
// Transport).
func (r *Relay) SetCoordinator(c deliverer) { r.coord = c }

// ---- swapnet.Transport: route maker→browser sends to the session inbox --------

// Send delivers env to peer. A "browser:<swapID>" peer is the synthetic handle of
// a browser taker: the message is queued for that browser to long-poll. Any other
// peer is a real p2p counterparty and falls through to the inner transport.
func (r *Relay) Send(peer string, env *swapnet.Envelope) error {
	if sid, ok := browserSwapID(peer); ok {
		r.mu.Lock()
		s := r.sessions[sid]
		if s != nil {
			s.touched = time.Now()
		}
		r.mu.Unlock()
		if s == nil {
			return errors.New("swaprelay: no browser session for " + sid)
		}
		select {
		case s.inbox <- msg{kind: env.Kind, payload: append([]byte(nil), env.Payload...)}:
			return nil
		default:
			return errors.New("swaprelay: browser inbox full for " + sid)
		}
	}
	return r.inner.Send(peer, env)
}

// ResolvePeer forwards to the inner transport's PeerResolver if it has one (real
// p2p reconnects); browser sessions never go stale, so this is a passthrough.
func (r *Relay) ResolvePeer(stale string) (string, bool) {
	if pr, ok := r.inner.(swapnet.PeerResolver); ok {
		return pr.ResolvePeer(stale)
	}
	return "", false
}

// ---- browser-facing API (driven by the HTTP relay handlers) ------------------

// Open registers a browser session for swapIDHex (idempotent). Must be called
// before Submit so the maker's replies have an inbox to land in. It first evicts
// idle-expired sessions and rejects once the concurrency ceiling is reached.
func (r *Relay) Open(swapIDHex string) error {
	if !validSwapID(swapIDHex) {
		return errors.New("swaprelay: bad swap id")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evictExpiredLocked()
	if s, ok := r.sessions[swapIDHex]; ok {
		s.touched = time.Now()
		return nil
	}
	if len(r.sessions) >= maxSessions {
		return errors.New("swaprelay: too many active swap sessions — try again later")
	}
	r.sessions[swapIDHex] = &session{inbox: make(chan msg, inboxCap), touched: time.Now()}
	return nil
}

// evictExpiredLocked drops sessions idle longer than sessionTTL. Caller holds mu.
func (r *Relay) evictExpiredLocked() {
	cutoff := time.Now().Add(-sessionTTL)
	for id, s := range r.sessions {
		if s.touched.Before(cutoff) {
			delete(r.sessions, id)
		}
	}
}

// Submit injects a browser-sent swap envelope into the coordinator as if it came
// from the "browser:<swapID>" peer. The first one (an Init) opens the maker
// session; later ones route to it by SwapID.
func (r *Relay) Submit(swapIDHex string, kind byte, payload []byte) error {
	if r.coord == nil {
		return errors.New("swaprelay: coordinator not wired")
	}
	if !validSwapID(swapIDHex) {
		return errors.New("swaprelay: bad swap id")
	}
	r.mu.Lock()
	s, known := r.sessions[swapIDHex]
	if known {
		s.touched = time.Now()
	}
	r.mu.Unlock()
	if !known {
		return errors.New("swaprelay: session not open (call open first)")
	}
	var id [32]byte
	b, _ := hex.DecodeString(swapIDHex)
	copy(id[:], b)
	env := &swapnet.Envelope{SwapID: id, Kind: swapnet.Kind(kind), Payload: payload}
	r.coord.Deliver(BrowserPrefix+swapIDHex, env.Serialize())
	return nil
}

// Recv blocks (until ctx is done) for the next maker→browser message on the
// session. ok=false means the session is unknown or ctx expired (the browser
// should retry the long-poll).
func (r *Relay) Recv(ctx context.Context, swapIDHex string) (kind byte, payload []byte, ok bool) {
	r.mu.Lock()
	s := r.sessions[swapIDHex]
	if s != nil {
		s.touched = time.Now()
	}
	r.mu.Unlock()
	if s == nil {
		return 0, nil, false
	}
	select {
	case m := <-s.inbox:
		return byte(m.kind), m.payload, true
	case <-ctx.Done():
		return 0, nil, false
	}
}

// Close drops a browser session (its inbox is GC'd). Safe to call repeatedly.
func (r *Relay) Close(swapIDHex string) {
	r.mu.Lock()
	delete(r.sessions, swapIDHex)
	r.mu.Unlock()
}

func browserSwapID(peer string) (string, bool) {
	if strings.HasPrefix(peer, BrowserPrefix) {
		return strings.TrimPrefix(peer, BrowserPrefix), true
	}
	return "", false
}

func validSwapID(hexID string) bool {
	if len(hexID) != 64 {
		return false
	}
	_, err := hex.DecodeString(hexID)
	return err == nil
}
