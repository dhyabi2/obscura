package swapbook

import (
	"testing"
	"time"

	"filippo.io/edwards25519"

	"obscura/pkg/config"
)

// minOrderSizeOverride temporarily sets the dust floor for an asset and returns a
// restore func.
func minOrderSizeOverride(t *testing.T, asset string, floor uint64) func() {
	t.Helper()
	prev, had := config.MinOrderSize[asset]
	config.MinOrderSize[asset] = floor
	return func() {
		if had {
			config.MinOrderSize[asset] = prev
		} else {
			delete(config.MinOrderSize, asset)
		}
	}
}

// makerOffer builds and signs a maker OBX->XNO offer (maker gives `give` OBX,
// wants `get` XNO) under a fresh key, with a generous TTL.
func makerOffer(t *testing.T, give, get uint64) (*Offer, *edwards25519.Scalar) {
	t.Helper()
	sec := randScalar(t)
	o := &Offer{
		GiveAsset:  "OBX",
		GetAsset:   "XNO",
		GiveAmount: give,
		GetAmount:  get,
		Expiry:     time.Now().Add(time.Hour).Unix(),
	}
	o.Sign(sec)
	return o, sec
}

// addOffer admits an offer or fails the test.
func addOffer(t *testing.T, b *Book, o *Offer) {
	t.Helper()
	if _, err := b.Add(o); err != nil {
		t.Fatalf("Add: %v", err)
	}
}

// A taker on the OBX/XNO pair GIVES XNO and GETS OBX. So Reserve("XNO","OBX",size).

func TestReserveWalksMultipleRungsAndDecrements(t *testing.T) {
	b := NewBook()
	// Two rungs at different rates. Best taker rate = most OBX per XNO = give/get.
	// o1: give 100 OBX for 100 XNO (rate 1.0) ; o2: give 100 OBX for 200 XNO (rate 0.5).
	o1, _ := makerOffer(t, 100, 100)
	o2, _ := makerOffer(t, 100, 200)
	addOffer(t, b, o1)
	addOffer(t, b, o2)

	// Reserve 150 XNO. o1 absorbs 100 XNO -> 100 OBX; o2 absorbs 50 XNO -> 25 OBX.
	res, getOut, giveIn, err := b.Reserve("XNO", "OBX", 150, ReserveOpts{})
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("want 2 rungs, got %d", len(res))
	}
	if giveIn != 150 {
		t.Fatalf("giveIn want 150, got %d", giveIn)
	}
	if getOut != 125 {
		t.Fatalf("getOut want 125, got %d", getOut)
	}
	// best rung (o1) is first.
	if res[0].OfferID != o1.ID() {
		t.Fatal("best rung should be o1 (rate 1.0)")
	}
	// o1 fully consumed (filled), o2 partial.
	fs1, _ := b.OfferFill(o1.ID())
	if fs1.Status != StatusFilled || fs1.RemainingGet != 0 {
		t.Fatalf("o1 should be filled, got %v rem=%d", fs1.Status, fs1.RemainingGet)
	}
	fs2, _ := b.OfferFill(o2.ID())
	if fs2.Status != StatusPartial || fs2.RemainingGet != 150 {
		t.Fatalf("o2 should be partial with 150 XNO left, got %v rem=%d", fs2.Status, fs2.RemainingGet)
	}
}

func TestPartialFillLeavesRemaining(t *testing.T) {
	b := NewBook()
	o, _ := makerOffer(t, 100, 100)
	addOffer(t, b, o)
	res, _, giveIn, err := b.Reserve("XNO", "OBX", 40, ReserveOpts{})
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if giveIn != 40 || len(res) != 1 {
		t.Fatalf("want 40/1 rung, got %d/%d", giveIn, len(res))
	}
	fs, _ := b.OfferFill(o.ID())
	if fs.Status != StatusPartial || fs.RemainingGet != 60 || fs.RemainingGive != 60 {
		t.Fatalf("partial state wrong: %v get=%d give=%d", fs.Status, fs.RemainingGet, fs.RemainingGive)
	}
}

func TestFOKAbortsWhenBookTooThin(t *testing.T) {
	b := NewBook()
	o, _ := makerOffer(t, 100, 100)
	addOffer(t, b, o)
	// Ask for 150 XNO FOK but only 100 available -> abort, nothing reserved.
	_, _, _, err := b.Reserve("XNO", "OBX", 150, ReserveOpts{FOK: true})
	if err != ErrFOKUnfillable {
		t.Fatalf("want ErrFOKUnfillable, got %v", err)
	}
	fs, _ := b.OfferFill(o.ID())
	if fs.Status != StatusOpen || fs.RemainingGet != 100 {
		t.Fatalf("FOK rollback failed: %v rem=%d", fs.Status, fs.RemainingGet)
	}
}

func TestIOCPartial(t *testing.T) {
	b := NewBook()
	o, _ := makerOffer(t, 100, 100)
	addOffer(t, b, o)
	// IOC: fill what's available (100), drop the rest — no error.
	res, _, giveIn, err := b.Reserve("XNO", "OBX", 250, ReserveOpts{})
	if err != nil {
		t.Fatalf("IOC Reserve: %v", err)
	}
	if giveIn != 100 || len(res) != 1 {
		t.Fatalf("IOC want 100 filled, got %d (%d rungs)", giveIn, len(res))
	}
}

func TestSelfTradeSkipped(t *testing.T) {
	b := NewBook()
	o, sec := makerOffer(t, 100, 100)
	addOffer(t, b, o)
	takerPub := new(edwards25519.Point).ScalarBaseMult(sec).Bytes()
	// Taker is the same maker -> STP skips the only offer -> no liquidity.
	_, _, _, err := b.Reserve("XNO", "OBX", 50, ReserveOpts{TakerPub: takerPub})
	if err != ErrNoLiquidity {
		t.Fatalf("want ErrNoLiquidity (self-trade skipped), got %v", err)
	}
	// A different taker fills fine.
	other := randScalar(t)
	otherPub := new(edwards25519.Point).ScalarBaseMult(other).Bytes()
	if _, _, giveIn, err := b.Reserve("XNO", "OBX", 50, ReserveOpts{TakerPub: otherPub}); err != nil || giveIn != 50 {
		t.Fatalf("non-self taker should fill 50, got %d %v", giveIn, err)
	}
}

func TestReleaseRestoresRemaining(t *testing.T) {
	b := NewBook()
	o, _ := makerOffer(t, 100, 100)
	addOffer(t, b, o)
	res, _, _, err := b.Reserve("XNO", "OBX", 70, ReserveOpts{})
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	b.ReleaseReservation(res)
	fs, _ := b.OfferFill(o.ID())
	if fs.Status != StatusOpen || fs.RemainingGet != 100 || fs.RemainingGive != 100 {
		t.Fatalf("release should restore to open/full, got %v get=%d give=%d", fs.Status, fs.RemainingGet, fs.RemainingGive)
	}
	// double-release must not inflate beyond full.
	b.ReleaseReservation(res)
	fs, _ = b.OfferFill(o.ID())
	if fs.RemainingGet != 100 || fs.RemainingGive != 100 {
		t.Fatalf("double-release inflated remaining: get=%d give=%d", fs.RemainingGet, fs.RemainingGive)
	}
}

func TestCommitTradeAppendsTapeAndLastPrice(t *testing.T) {
	b := NewBook()
	o, _ := makerOffer(t, 100, 100)
	addOffer(t, b, o)
	res, _, _, err := b.Reserve("XNO", "OBX", 50, ReserveOpts{})
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	tr := b.CommitTrade(res, "XNO", "OBX", "deadbeef", "cafe")
	if tr.Give != 50 || tr.Get != 50 || tr.Pair != "XNO/OBX" || tr.SwapKey != "deadbeef" {
		t.Fatalf("trade fields wrong: %+v", tr)
	}
	tape := b.Trades("XNO/OBX", 10)
	if len(tape) != 1 || tape[0].SwapKey != "deadbeef" {
		t.Fatalf("tape should have 1 trade, got %d", len(tape))
	}
	lp, ok := b.LastPrice("XNO/OBX")
	if !ok || lp != "1" {
		t.Fatalf("last price want 1, got %q ok=%v", lp, ok)
	}
}

func TestCandlesAndStatsAggregate(t *testing.T) {
	b := NewBook()
	s := b.sc()
	now := time.Now().Unix()
	// Hand-build three trades in one hour bucket: prices 1, 2, then 1.5 (close).
	s.trades = []Trade{
		{Pair: "XNO/OBX", Price: "1", Give: 100, Get: 100, Time: now - 30},
		{Pair: "XNO/OBX", Price: "2", Give: 50, Get: 100, Time: now - 20},
		{Pair: "XNO/OBX", Price: "1.5", Give: 200, Get: 300, Time: now - 10},
	}
	candles := b.Candles("XNO/OBX", 3600, 10)
	if len(candles) != 1 {
		t.Fatalf("want 1 candle, got %d", len(candles))
	}
	c := candles[0]
	if c.Open != "1" || c.High != "2" || c.Low != "1" || c.Close != "1.5" {
		t.Fatalf("OHLC wrong: %+v", c)
	}
	if c.Volume != 350 || c.Trades != 3 {
		t.Fatalf("volume/count wrong: vol=%d n=%d", c.Volume, c.Trades)
	}
	st := b.Stats24h("XNO/OBX")
	if st.Volume != 350 || st.VolumeGet != 500 || st.Trades != 3 {
		t.Fatalf("24h volume wrong: %+v", st)
	}
	if st.High != "2" || st.Low != "1" || st.Open != "1" || st.Last != "1.5" {
		t.Fatalf("24h OHLC wrong: %+v", st)
	}
}

func TestStats24hWindowExcludesOld(t *testing.T) {
	b := NewBook()
	s := b.sc()
	now := time.Now().Unix()
	s.trades = []Trade{
		{Pair: "XNO/OBX", Price: "5", Give: 10, Get: 50, Time: now - 48*3600}, // outside window
		{Pair: "XNO/OBX", Price: "1", Give: 100, Get: 100, Time: now - 60},    // inside
	}
	st := b.Stats24h("XNO/OBX")
	if st.Volume != 100 || st.Trades != 1 {
		t.Fatalf("window should exclude old trade: %+v", st)
	}
	if st.Open != "1" || st.High != "1" {
		t.Fatalf("windowed open/high wrong: %+v", st)
	}
	if st.Last != "1" { // last is overall-last; both? the inside one is chronologically last
		t.Fatalf("last want 1, got %q", st.Last)
	}
}

func TestDoubleTakeCannotOversell(t *testing.T) {
	b := NewBook()
	o, _ := makerOffer(t, 100, 100)
	addOffer(t, b, o)
	// First take reserves all 100 XNO.
	r1, _, g1, err := b.Reserve("XNO", "OBX", 100, ReserveOpts{})
	if err != nil || g1 != 100 {
		t.Fatalf("first reserve: %d %v", g1, err)
	}
	_ = r1
	// Second take must find nothing left (offer is filled).
	_, _, _, err = b.Reserve("XNO", "OBX", 100, ReserveOpts{})
	if err != ErrNoLiquidity {
		t.Fatalf("second reserve should find no liquidity, got %v", err)
	}
}

func TestConcurrentReserveNoOversell(t *testing.T) {
	b := NewBook()
	o, _ := makerOffer(t, 1000, 1000)
	addOffer(t, b, o)
	const goroutines = 20
	results := make(chan uint64, goroutines)
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		go func() {
			<-start
			_, _, g, err := b.Reserve("XNO", "OBX", 100, ReserveOpts{})
			if err != nil {
				results <- 0
				return
			}
			results <- g
		}()
	}
	close(start)
	var total uint64
	for i := 0; i < goroutines; i++ {
		total += <-results
	}
	// 20 goroutines each want 100 XNO; only 1000 available -> total reserved <= 1000.
	if total > 1000 {
		t.Fatalf("oversold: total reserved %d > 1000", total)
	}
	fs, _ := b.OfferFill(o.ID())
	if uint64(1000)-fs.RemainingGet != total {
		t.Fatalf("decrement (%d) != reserved (%d)", 1000-fs.RemainingGet, total)
	}
}

func TestPostOnlyRejectsCross(t *testing.T) {
	b := NewBook()
	// Existing maker: gives 100 OBX for 100 XNO (rate 1.0).
	o1, _ := makerOffer(t, 100, 100)
	addOffer(t, b, o1)
	// A post-only XNO->OBX maker that gives 100 XNO for 90 OBX would let a taker
	// round-trip (cross), so it must be rejected.
	crossing := &Offer{GiveAsset: "XNO", GetAsset: "OBX", GiveAmount: 100, GetAmount: 90,
		Expiry: time.Now().Add(time.Hour).Unix()}
	crossing.Sign(randScalar(t))
	if _, err := b.AddPostOnly(crossing); err == nil {
		t.Fatal("post-only crossing offer should be rejected")
	}
	// A non-crossing post-only maker (asks more OBX than the book gives) is admitted.
	passive := &Offer{GiveAsset: "XNO", GetAsset: "OBX", GiveAmount: 100, GetAmount: 200,
		Expiry: time.Now().Add(time.Hour).Unix()}
	passive.Sign(randScalar(t))
	if ok, err := b.AddPostOnly(passive); err != nil || !ok {
		t.Fatalf("non-crossing post-only should be admitted, got ok=%v err=%v", ok, err)
	}
}

func TestDustRejected(t *testing.T) {
	b := NewBook()
	o, _ := makerOffer(t, 100, 100)
	addOffer(t, b, o)
	// MinOrderSize for XNO is 1 by default; set a higher floor to exercise dust.
	saved := minOrderSizeOverride(t, "XNO", 10)
	defer saved()
	if _, _, _, err := b.Reserve("XNO", "OBX", 5, ReserveOpts{}); err != ErrDust {
		t.Fatalf("want ErrDust for size 5 < floor 10, got %v", err)
	}
	if _, _, g, err := b.Reserve("XNO", "OBX", 20, ReserveOpts{}); err != nil || g != 20 {
		t.Fatalf("size 20 >= floor should fill, got %d %v", g, err)
	}
}

func TestSlippageCapStops(t *testing.T) {
	b := NewBook()
	o1, _ := makerOffer(t, 100, 100) // rate 1.0
	o2, _ := makerOffer(t, 100, 200) // rate 0.5
	addOffer(t, b, o1)
	addOffer(t, b, o2)
	// Cap min rate at 0.75 (3/4): only o1 qualifies, o2 (0.5) is skipped.
	res, _, giveIn, err := b.Reserve("XNO", "OBX", 200, ReserveOpts{MinFillRateNum: 3, MinFillRateDen: 4})
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if len(res) != 1 || res[0].OfferID != o1.ID() || giveIn != 100 {
		t.Fatalf("slippage cap should keep only o1 (100 XNO), got %d rungs giveIn=%d", len(res), giveIn)
	}
}
