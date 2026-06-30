package swapbook

import "testing"

// TestTradingChartReflectsPriceChange proves the data the explorer's TRADING-VIEW
// chart consumes (the candle series + last price + trade tape, from /candles and
// /trades) CHANGES when a trade settles at a new price. It captures the chart
// state, causes a real price change (a second trade at a different rate), and
// asserts the chart-facing data moved accordingly — i.e. a new trade visibly
// updates the chart, not just the tape.
func TestTradingChartReflectsPriceChange(t *testing.T) {
	b := NewBook()

	// --- trade #1 at rate 1 (offer: 100 OBX for 100 XNO) -> price "1" ---
	o1, _ := makerOffer(t, 100, 100)
	addOffer(t, b, o1)
	res1, _, _, err := b.Reserve("XNO", "OBX", 100, ReserveOpts{}) // consume offer1 fully
	if err != nil {
		t.Fatalf("Reserve #1: %v", err)
	}
	b.CommitTrade(res1, "XNO", "OBX", "swapkey1", "taker1")

	// snapshot the chart state BEFORE the price change
	candlesA := b.Candles("XNO/OBX", 3600, 10)
	lastA, okA := b.LastPrice("XNO/OBX")
	tradesA := len(b.Trades("XNO/OBX", 100))
	if !okA || len(candlesA) == 0 {
		t.Fatalf("expected a candle + last price after the first trade (last=%q ok=%v candles=%d)", lastA, okA, len(candlesA))
	}
	closeA := candlesA[len(candlesA)-1].Close

	// --- cause a PRICE CHANGE: trade #2 at rate 2 (offer: 100 OBX for 200 XNO) ---
	o2, _ := makerOffer(t, 100, 200)
	addOffer(t, b, o2)
	res2, _, _, err := b.Reserve("XNO", "OBX", 100, ReserveOpts{})
	if err != nil {
		t.Fatalf("Reserve #2: %v", err)
	}
	b.CommitTrade(res2, "XNO", "OBX", "swapkey2", "taker2")

	// snapshot AFTER
	candlesB := b.Candles("XNO/OBX", 3600, 10)
	lastB, okB := b.LastPrice("XNO/OBX")
	tradesB := len(b.Trades("XNO/OBX", 100))
	if !okB || len(candlesB) == 0 {
		t.Fatal("expected a candle + last price after the second trade")
	}
	closeB := candlesB[len(candlesB)-1].Close

	// the trade tape grew (the chart gains a data point)
	if tradesB <= tradesA {
		t.Fatalf("trade tape did not grow: before=%d after=%d", tradesA, tradesB)
	}
	// the last price (the chart's headline + latest point) changed
	if lastB == lastA {
		t.Fatalf("last price unchanged after a price change (still %q) — chart would not move", lastB)
	}
	// the candle CLOSE (the chart's most-recent value) reflects the new price
	if closeB == closeA {
		t.Fatalf("candle close unchanged (%q) — the trading chart would not reflect the new trade", closeB)
	}
	// the OHLC range must widen once a second, differently-priced trade lands in
	// the bucket (high and low can no longer be equal to the single first price).
	cB := candlesB[len(candlesB)-1]
	if cB.High == cB.Low {
		t.Fatalf("candle OHLC range did not widen after a differently-priced trade (high==low==%q)", cB.High)
	}
	t.Logf("chart data moved: last %q -> %q, close %q -> %q, OHLC[%s..%s], trades %d -> %d",
		lastA, lastB, closeA, closeB, cB.Low, cB.High, tradesA, tradesB)
}
