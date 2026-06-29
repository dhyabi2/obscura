package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"

	"filippo.io/edwards25519"

	"obscura/pkg/config"
	"obscura/pkg/swapbook"
)

// client is the simulator's typed view of the node's swap JSON-RPC. Every method
// here maps to a real route the node serves (/offer, /offer/cancel, /offers/json,
// /depth, /quote, /trades, /swaps/take, /liquidity), so the simulator drives the
// LIVE book exactly as a real participant would — there is no faked ledger.
type client struct {
	rpc    string
	hc     *http.Client
	decOBX int // OBX offer-unit decimals (12)
	decXNO int // XNO offer-unit decimals (12)
}

func newClient(rpcURL string, timeout time.Duration) *client {
	return &client{
		rpc:    rpcURL,
		hc:     &http.Client{Timeout: timeout},
		decOBX: config.AutoLiquidityDecimals["OBX"],
		decXNO: config.AutoLiquidityDecimals["XNO"],
	}
}

// ---- unit conversion --------------------------------------------------------

// obxAtomic converts a human OBX amount to atomic offer-units at OBX decimals.
func (c *client) obxAtomic(obx float64) uint64 {
	if obx <= 0 {
		return 0
	}
	return uint64(obx * math.Pow10(c.decOBX))
}

// xnoFor returns the XNO offer-units that obxAtomic OBX are worth at `rate`
// (XNO per OBX). Never returns 0 for a positive input (Verify rejects zeros).
func (c *client) xnoFor(obxAtomic uint64, rate float64) uint64 {
	obxHuman := float64(obxAtomic) / math.Pow10(c.decOBX)
	v := uint64(obxHuman * rate * math.Pow10(c.decXNO))
	if v == 0 {
		v = 1
	}
	return v
}

// ---- writes -----------------------------------------------------------------

// postOffer builds, signs, and POSTs a maker offer; returns its id on success.
// give/get are asset tickers ("OBX"/"XNO"); amounts are atomic offer-units.
func (c *client) postOffer(secret *edwards25519.Scalar, give, get string, giveAmt, getAmt uint64, ttl time.Duration) ([32]byte, bool) {
	if giveAmt == 0 || getAmt == 0 {
		return [32]byte{}, false
	}
	o := swapbook.BuildSignedOffer(give, get, giveAmt, getAmt, ttl, secret)
	body, _ := json.Marshal(map[string]string{"offer": hex.EncodeToString(o.Serialize())})
	resp, err := c.hc.Post(c.rpc+"/offer", "application/json", bytes.NewReader(body))
	if err != nil {
		return [32]byte{}, false
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return [32]byte{}, false
	}
	return o.ID(), true
}

// cancel signs and POSTs a maker-authenticated cancellation for one offer id.
func (c *client) cancel(secret *edwards25519.Scalar, id [32]byte) error {
	sig := swapbook.SignCancel(secret, id)
	body, _ := json.Marshal(map[string]string{
		"offer_id": hex.EncodeToString(id[:]),
		"sig":      hex.EncodeToString(sig),
	})
	resp, err := c.hc.Post(c.rpc+"/offer/cancel", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer drain(resp)
	return nil
}

// takeResult is the parsed outcome of a /swaps/take call.
type takeResult struct {
	SwapID   string `json:"swap_id"`
	Reserved string `json:"reserved"` // XNO offer-units actually reserved
	GetOut   string `json:"get_out"`  // OBX offer-units the taker receives
	Error    string `json:"error"`
}

// take executes a REAL fill: a taker giving `size` XNO offer-units walks the book
// for OBX via /swaps/take (which reserves + commits a real trade to the tape).
// orderType is "" / "market" / "ioc" / "fok". A maker offer is matched, reserved,
// and (async) settled by the node; on success it shows up on /trades.
func (c *client) take(size uint64, orderType, takerPubHex string) (takeResult, error) {
	var tr takeResult
	if size == 0 {
		return tr, fmt.Errorf("zero take size")
	}
	body, _ := json.Marshal(map[string]any{
		"size":      size,
		"type":      orderType,
		"taker_pub": takerPubHex,
	})
	resp, err := c.hc.Post(c.rpc+"/swaps/take", "application/json", bytes.NewReader(body))
	if err != nil {
		return tr, err
	}
	defer drain(resp)
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return tr, err
	}
	if tr.Error != "" {
		return tr, fmt.Errorf("take: %s", tr.Error)
	}
	return tr, nil
}

// ---- reads ------------------------------------------------------------------

type depthRung struct {
	Rate    string `json:"rate"`
	Give    string `json:"give"`
	Get     string `json:"get"`
	CumGive string `json:"cum_give"`
	CumGet  string `json:"cum_get"`
	OfferID string `json:"offer_id"`
}

type depthResp struct {
	Give  string      `json:"give"`
	Get   string      `json:"get"`
	Rungs []depthRung `json:"rungs"`
}

// depth returns the cumulative taker ladder for giving `give` to get `get`,
// best-rate-first (rung 0 is best for the taker). rate = get per give.
func (c *client) depth(give, get string) []depthRung {
	var dr depthResp
	if c.getJSON(fmt.Sprintf("/depth?give=%s&get=%s", give, get), &dr) != nil {
		return nil
	}
	return dr.Rungs
}

type quoteResp struct {
	Filled string `json:"filled"`
	GetOut string `json:"get_out"`
	VWAP   string `json:"vwap"`
	Full   bool   `json:"full"`
}

// quote prices (without executing) a taker giving `size` of `give` for `get`.
func (c *client) quote(give, get string, size uint64) (quoteResp, error) {
	var qr quoteResp
	err := c.getJSON(fmt.Sprintf("/quote?give=%s&get=%s&size=%d", give, get, size), &qr)
	return qr, err
}

type tradeJSON struct {
	Pair  string `json:"pair"`
	Price string `json:"price"`
	Give  string `json:"give"`
	Get   string `json:"get"`
	Maker string `json:"maker"`
	Taker string `json:"taker"`
	Time  int64  `json:"time"`
}

type tradesResp struct {
	Pair      string      `json:"pair"`
	LastPrice string      `json:"last_price"`
	Trades    []tradeJSON `json:"trades"`
}

// trades returns the executed-trade tape for a pair (taker orientation
// "GIVE/GET", e.g. "XNO/OBX"), newest first, capped by limit.
func (c *client) trades(pair string, limit int) tradesResp {
	var tr tradesResp
	_ = c.getJSON(fmt.Sprintf("/trades?pair=%s&limit=%d", pair, limit), &tr)
	return tr
}

// liveCount returns the number of live offers on the whole book.
func (c *client) liveCount() int {
	var r struct {
		Offers []json.RawMessage `json:"offers"`
	}
	if c.getJSON("/offers/json", &r) != nil {
		return -1
	}
	return len(r.Offers)
}

func (c *client) getJSON(path string, v any) error {
	resp, err := c.hc.Get(c.rpc + path)
	if err != nil {
		return err
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

// ---- book observables -------------------------------------------------------

// bookView is a snapshot of the live top-of-book in XNO/OBX terms, derived from
// both directed depth ladders. It is what agents observe to decide their actions.
type bookView struct {
	bestBid float64 // highest XNO/OBX a maker pays to BUY OBX (taker SELLs OBX)
	bestAsk float64 // lowest XNO/OBX a taker pays to BUY OBX
	mid     float64
	// askDepthXNO is the XNO offer-units available across the SELL-OBX side
	// (the XNO->OBX taker ladder), used as a crude Kyle-depth proxy.
	askDepthXNO float64
	bidDepthXNO float64
	hasBid      bool
	hasAsk      bool
}

// observe builds a bookView from the two directed depth ladders.
//
//	XNO->OBX ladder: taker gives XNO, gets OBX. rate = OBX per XNO.
//	  -> ask in XNO/OBX = 1/rate; rung 0 is the LOWEST ask (best for taker).
//	OBX->XNO ladder: taker gives OBX, gets XNO. rate = XNO per OBX directly.
//	  -> that IS a bid in XNO/OBX; rung 0 is the HIGHEST bid (best for taker).
func (c *client) observe() bookView {
	var bv bookView
	xnoObx := c.depth("XNO", "OBX") // SELL-OBX makers (asks)
	obxXno := c.depth("OBX", "XNO") // BUY-OBX makers (bids)

	if len(xnoObx) > 0 {
		var r float64
		fmt.Sscanf(xnoObx[0].Rate, "%g", &r)
		if r > 0 {
			bv.bestAsk = 1 / r
			bv.hasAsk = true
		}
		// cumulative XNO the SELL side can absorb = last rung cum_give (give=XNO).
		bv.askDepthXNO = parseUnits(xnoObx[len(xnoObx)-1].CumGive, c.decXNO)
	}
	if len(obxXno) > 0 {
		var r float64
		fmt.Sscanf(obxXno[0].Rate, "%g", &r)
		if r > 0 {
			bv.bestBid = r
			bv.hasBid = true
		}
		bv.bidDepthXNO = parseUnits(obxXno[len(obxXno)-1].CumGet, c.decXNO)
	}
	if bv.hasBid && bv.hasAsk {
		bv.mid = (bv.bestBid + bv.bestAsk) / 2
	} else if bv.hasAsk {
		bv.mid = bv.bestAsk
	} else if bv.hasBid {
		bv.mid = bv.bestBid
	}
	return bv
}

// parseUnits parses an atomic-string at `dec` decimals into a human float.
func parseUnits(s string, dec int) float64 {
	var v float64
	fmt.Sscanf(s, "%g", &v)
	return v / math.Pow10(dec)
}
