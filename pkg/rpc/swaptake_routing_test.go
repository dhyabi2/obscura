package rpc

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"

	"filippo.io/edwards25519"

	"obscura/pkg/commit"
	"obscura/pkg/swapbook"
	"obscura/pkg/swapnet"
)

// testMakerSecret returns a deterministic maker signing scalar for the routing test.
func testMakerSecret(t *testing.T) *edwards25519.Scalar {
	t.Helper()
	return commit.HashToScalar([]byte("rpc/route/maker"), []byte("seed"))
}

// timeNowPlus1h is an offer expiry 1h out (well inside MaxOfferTTL).
func timeNowPlus1h() int64 { return time.Now().Add(time.Hour).Unix() }

// ---- stubs for the /swaps/take routing test --------------------------------

// routeOffers is a minimal OfferProvider holding ONE live OBX/XNO offer, with a
// Reserve that returns a single rung tagged with the offer's maker (so the take
// path resolves the peer from best.Maker). Everything else is a no-op.
type routeOffers struct {
	offer    *swapbook.Offer
	released bool
}

func (r *routeOffers) Offers() []*swapbook.Offer { return []*swapbook.Offer{r.offer} }
func (r *routeOffers) PostOffer(*swapbook.Offer) error { return nil }
func (r *routeOffers) Liquidity() ([]swapbook.PairLiquidity, int, int) { return nil, 0, 0 }
func (r *routeOffers) Cancel([32]byte, []byte) error { return nil }
func (r *routeOffers) Quote(string, string, uint64) (uint64, uint64, float64, int, bool) {
	return 0, 0, 0, 0, false
}
func (r *routeOffers) Depth(string, string) []swapbook.DepthLevel { return nil }
func (r *routeOffers) Reserve(give, get string, size uint64, _ swapbook.ReserveOpts) ([]swapbook.Reservation, uint64, uint64, error) {
	res := []swapbook.Reservation{{
		OfferID: r.offer.ID(),
		Maker:   r.offer.Maker,
		Pay:     size,
		Recv:    r.offer.GiveAmount,
	}}
	return res, r.offer.GiveAmount, size, nil
}
func (r *routeOffers) CommitTrade([]swapbook.Reservation, string, string, string, string) swapbook.Trade {
	return swapbook.Trade{}
}
func (r *routeOffers) ReleaseReservation([]swapbook.Reservation) { r.released = true }
func (r *routeOffers) Trades(string, int) []swapbook.Trade        { return nil }
func (r *routeOffers) LastPrice(string) (string, bool)            { return "", false }
func (r *routeOffers) Candles(string, int64, int) []swapbook.Candle { return nil }
func (r *routeOffers) Stats24h(string) swapbook.Stats24h          { return swapbook.Stats24h{} }
func (r *routeOffers) MakerOffers([]byte) []*swapbook.Offer       { return nil }
func (r *routeOffers) OfferFill([32]byte) (swapbook.FillState, bool) {
	return swapbook.FillState{}, false
}

// routePeers is a PeerProvider that resolves PeerForMaker to a fixed handle when the
// directory is "populated", or reports unknown when not.
type routePeers struct {
	addrs    []string
	makerKey string // hex of the maker we know a peer for ("" = know nobody)
	peer     string
}

func (p routePeers) PeerCount() int      { return len(p.addrs) }
func (p routePeers) PeerAddrs() []string { return p.addrs }
func (p routePeers) PeerForMaker(maker []byte) (string, bool) {
	if p.makerKey != "" && hex.EncodeToString(maker) == p.makerKey {
		return p.peer, true
	}
	return "", false
}

// routeCoord is a SwapCoordinator stub that records the peer Take was called with.
type routeCoord struct {
	takenPeer string
	called    bool
}

func (c *routeCoord) ActiveSessions() []swapnet.SessionView { return nil }
func (c *routeCoord) Take(peer string, _ uint64, _ *big.Int, _ uint64) (*swapnet.Session, error) {
	c.called, c.takenPeer = true, peer
	// Return a non-nil session whose Wait/Succeeded are usable; the simplest is the
	// real type via a never-resolving handle is not constructible here, so return an
	// error AFTER recording the peer so the handler stops without a nil deref. The
	// routing decision (the peer) is already captured, which is what we assert.
	return nil, errStubTakeStop
}

type stubErr string

func (e stubErr) Error() string { return string(e) }

const errStubTakeStop = stubErr("stub: take recorded, stopping")

// makeRouteOffer builds a signed live OBX/XNO offer for the routing test.
func makeRouteOffer(t *testing.T) *swapbook.Offer {
	t.Helper()
	o := &swapbook.Offer{
		GiveAsset: "OBX", GetAsset: "XNO",
		GiveAmount: 5, GetAmount: 5,
		Expiry: timeNowPlus1h(),
	}
	o.Sign(testMakerSecret(t))
	return o
}

// TestSwapsTakeRoutesToMakerPeer proves /swaps/take resolves the maker peer from the
// taken offer's maker (PeerForMaker) instead of PeerAddrs()[0], and rejects when the
// maker peer is unknown and no ?peer override is given.
func TestSwapsTakeRoutesToMakerPeer(t *testing.T) {
	offer := makeRouteOffer(t)
	makerHex := hex.EncodeToString(offer.Maker)

	// (a) maker peer KNOWN via the directory → Take must route to it, NOT to the first
	// connected peer (which is a DIFFERENT, wrong address).
	{
		s := newTestServer(t)
		of := &routeOffers{offer: offer}
		coord := &routeCoord{}
		s.SetOfferBook(of)
		s.SetPeerProvider(routePeers{addrs: []string{"wrong-first-peer:1"}, makerKey: makerHex, peer: "maker-peer:9"})
		s.SetSwapCoordinator(coord, 1)
		h := s.Handler()

		body := `{"offer_id":"` + hex.EncodeToString(idOf(offer)) + `"}`
		w := doBody(h, "POST", "/swaps/take", "127.0.0.1:1", "", body)
		_ = w
		if !coord.called {
			t.Fatal("Take was not called")
		}
		if coord.takenPeer != "maker-peer:9" {
			t.Fatalf("Take routed to %q, want the maker's directory peer maker-peer:9 (NOT PeerAddrs()[0])", coord.takenPeer)
		}
	}

	// (b) maker peer UNKNOWN + no override → reject, and DO NOT call Take or leak the
	// reservation (it must be released).
	{
		s := newTestServer(t)
		of := &routeOffers{offer: offer}
		coord := &routeCoord{}
		s.SetOfferBook(of)
		s.SetPeerProvider(routePeers{addrs: []string{"some-peer:1"}}) // knows nobody
		s.SetSwapCoordinator(coord, 1)
		h := s.Handler()

		body := `{"offer_id":"` + hex.EncodeToString(idOf(offer)) + `"}`
		w := doBody(h, "POST", "/swaps/take", "127.0.0.1:1", "", body)
		if coord.called {
			t.Fatal("Take must NOT be called when the maker peer is unknown")
		}
		var resp map[string]string
		_ = json.NewDecoder(w.Body).Decode(&resp)
		if resp["error"] == "" || !strings.Contains(resp["error"], "maker peer unknown") {
			t.Fatalf("expected 'maker peer unknown' error, got %v", resp)
		}
		if !of.released {
			t.Fatal("reservation was not released after rejecting an unroutable take")
		}
	}

	// (c) explicit ?peer override wins even when the directory is empty.
	{
		s := newTestServer(t)
		of := &routeOffers{offer: offer}
		coord := &routeCoord{}
		s.SetOfferBook(of)
		s.SetPeerProvider(routePeers{}) // knows nobody, no peers
		s.SetSwapCoordinator(coord, 1)
		h := s.Handler()

		body := `{"offer_id":"` + hex.EncodeToString(idOf(offer)) + `","peer":"explicit:7"}`
		_ = doBody(h, "POST", "/swaps/take", "127.0.0.1:1", "", body)
		if !coord.called || coord.takenPeer != "explicit:7" {
			t.Fatalf("explicit peer override not honored: called=%v peer=%q", coord.called, coord.takenPeer)
		}
	}
}

func idOf(o *swapbook.Offer) []byte {
	id := o.ID()
	return id[:]
}
