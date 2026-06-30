//go:build js && wasm

package main

// Browser-side TAKER execution: runs pkg/takerdrive.RunTaker (which drives the
// UNCHANGED pkg/swapsession taker protocol) entirely inside WebAssembly. Every
// secret — the ephemeral swap shares a/sA/ra (minted inside swapsession) and the
// XNO funding key (signed via pkg/nanocrypto) — stays in the browser. The node is
// only a relay: it forwards swap envelopes to the maker over p2p and publishes the
// browser-signed Nano blocks. This is what makes the one-button swap non-custodial.
//
// The driver runs on its own goroutine; its TakerOBX / XNOLocker / Transport
// dependencies are backed by an injected JS "bridge" object whose methods each
// return a Promise. The Go adapters call those methods through await(), which
// blocks the DRIVER GOROUTINE (not the JS event loop) until the Promise settles —
// the standard Go-wasm pattern for synchronous-looking calls over async fetch.
//
// JS bridge contract (all methods return a Promise):
//
//	open(swapIdHex, paramsJSON)         -> open the relay session to the maker
//	send(kind:int, payloadHex)          -> deliver one swap envelope to the maker
//	recv()                              -> {kind:int, payload:hex} next inbound envelope
//	height()                            -> OBX chain height (number)
//	findSwapOut(swapKeyHex)             -> {found, claim_key, refund_key,
//	                                        unlock_height, claim_r, claim_t, amount}
//	submitTx(txHex)                     -> relay the signed OBX claim tx to the node
//	nanoLock(amountRawDec, accountAddr) -> lockIdHex (JS signs via xnoSignBlock +
//	                                        publishes via the node Nano relay)
//	nanoConfirmed(lockIdHex)            -> bool
//	phase(name)                         -> OPTIONAL progress hook (fire-and-forget)

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"syscall/js"

	"obscura/pkg/commit"
	"obscura/pkg/swap"
	"obscura/pkg/takerdrive"
	"obscura/pkg/tx"
)

// await calls bridge.<name>(args...) (which must return a Promise) and blocks the
// calling goroutine until it settles. MUST be called from the driver goroutine,
// never from a js.FuncOf handler on the main thread (that would deadlock).
func await(bridge js.Value, name string, args ...interface{}) (js.Value, error) {
	fn := bridge.Get(name)
	if fn.Type() != js.TypeFunction {
		return js.Undefined(), fmt.Errorf("swap bridge: method %q missing", name)
	}
	type result struct {
		v   js.Value
		err error
	}
	ch := make(chan result, 1)
	then := js.FuncOf(func(_ js.Value, a []js.Value) interface{} {
		var v js.Value
		if len(a) > 0 {
			v = a[0]
		}
		ch <- result{v, nil}
		return nil
	})
	catch := js.FuncOf(func(_ js.Value, a []js.Value) interface{} {
		msg := "swap bridge: " + name + " rejected"
		if len(a) > 0 {
			msg = a[0].Call("toString").String()
		}
		ch <- result{js.Undefined(), errors.New(msg)}
		return nil
	})
	defer then.Release()
	defer catch.Release()
	bridge.Call(name, args...).Call("then", then).Call("catch", catch)
	r := <-ch
	return r.v, r.err
}

// ---- bridge-backed swapsession capabilities --------------------------------

type wasmTransport struct{ bridge js.Value }

func (t wasmTransport) Send(kind takerdrive.Kind, payload []byte) error {
	_, err := await(t.bridge, "send", int(kind), hex.EncodeToString(payload))
	return err
}

func (t wasmTransport) Recv() (takerdrive.Kind, []byte, error) {
	v, err := await(t.bridge, "recv")
	if err != nil {
		return 0, nil, err
	}
	payload, err := hex.DecodeString(v.Get("payload").String())
	if err != nil {
		return 0, nil, fmt.Errorf("swap recv: bad payload hex: %w", err)
	}
	return takerdrive.Kind(v.Get("kind").Int()), payload, nil
}

type wasmTakerOBX struct{ bridge js.Value }

func (o wasmTakerOBX) Height() uint64 {
	v, err := await(o.bridge, "height")
	if err != nil {
		return 0
	}
	// height arrives as a JS number or decimal string; both parse safely well
	// below 2^53 for any realistic chain height.
	switch v.Type() {
	case js.TypeNumber:
		return uint64(v.Float())
	default:
		h, _ := strconv.ParseUint(v.String(), 10, 64)
		return h
	}
}

func (o wasmTakerOBX) FindSwapOut(swapKey []byte) (swap.SwapOutput, bool) {
	v, err := await(o.bridge, "findSwapOut", hex.EncodeToString(swapKey))
	if err != nil || v.IsNull() || v.IsUndefined() || !v.Get("found").Bool() {
		return swap.SwapOutput{}, false
	}
	hexb := func(name string) []byte { b, _ := hex.DecodeString(v.Get(name).String()); return b }
	amount, _ := strconv.ParseUint(v.Get("amount").String(), 10, 64)
	return swap.SwapOutput{
		ClaimKey:     hexb("claim_key"),
		RefundKey:    hexb("refund_key"),
		UnlockHeight: parseU64(v.Get("unlock_height")),
		ClaimR:       hexb("claim_r"),
		ClaimT:       hexb("claim_t"),
		Amount:       amount,
	}, true
}

func (o wasmTakerOBX) BuildClaim(swapKey []byte, amount, fee uint64) (*tx.Transaction, []byte, error) {
	var coreHash []byte
	t, err := w.BuildSwapSpend(swapKey, amount, false, fee, func(ch []byte) []byte {
		coreHash = append([]byte(nil), ch...)
		return make([]byte, 64) // placeholder; the real adapted sig is attached in MineClaim
	})
	if err != nil {
		return nil, nil, err
	}
	return t, coreHash, nil
}

func (o wasmTakerOBX) MineClaim(t *tx.Transaction, sig []byte) error {
	if len(t.SwapInputs) == 0 {
		return errors.New("swap claim: no swap input")
	}
	t.SwapInputs[0].Sig = sig
	_, err := await(o.bridge, "submitTx", hex.EncodeToString(t.Serialize()))
	return err
}

type wasmXNOLocker struct{ bridge js.Value }

func (n wasmXNOLocker) Lock(amount *big.Int, accountPub []byte) (string, error) {
	// Pass the joint account's PUBLIC KEY hex: a Nano send block's `link` field is
	// the destination public key, which JS uses verbatim when it builds+publishes
	// the lock send. (JS can derive the nano_ address from it if it needs to show
	// one.) accountPub = (sA+sB)·G, the 2-of-2 joint account.
	v, err := await(n.bridge, "nanoLock", amount.String(), hex.EncodeToString(accountPub))
	if err != nil {
		return "", err
	}
	return v.String(), nil
}

func (n wasmXNOLocker) Confirmed(lockID string) bool {
	v, err := await(n.bridge, "nanoConfirmed", lockID)
	return err == nil && v.Truthy()
}

func parseU64(v js.Value) uint64 {
	if v.Type() == js.TypeNumber {
		return uint64(v.Float())
	}
	h, _ := strconv.ParseUint(v.String(), 10, 64)
	return h
}

// ---- entry point -----------------------------------------------------------

// swapTakerRun(paramsJSON, bridge) -> Promise. paramsJSON = {obx_atomic, xno_raw,
// fee}. Mints a fresh unguessable SwapID, opens the relay session, and drives the
// full non-custodial taker swap on a goroutine. The Promise resolves with
// {ok:true, swap_id} or rejects with the failure.
func swapTakerRun(this js.Value, args []js.Value) any {
	if w == nil || len(seed) == 0 {
		return errObj("no wallet")
	}
	if len(args) < 2 {
		return errObj("need params, bridge")
	}
	var p struct {
		OBXAtomic string `json:"obx_atomic"`
		XNORaw    string `json:"xno_raw"`
		Fee       uint64 `json:"fee"`
	}
	if err := json.Unmarshal([]byte(args[0].String()), &p); err != nil {
		return errObj("bad params: " + err.Error())
	}
	obxAtomic, err := strconv.ParseUint(p.OBXAtomic, 10, 64)
	if err != nil {
		return errObj("bad obx_atomic")
	}
	xnoRaw, ok := new(big.Int).SetString(p.XNORaw, 10)
	if !ok || xnoRaw.Sign() < 0 {
		return errObj("bad xno_raw")
	}
	paramsJSON := args[0].String()
	bridge := args[1]

	var swapID [32]byte
	copy(swapID[:], commit.RandomScalar().Bytes())

	return newPromise(func(resolve, reject js.Value) {
		go func() {
			if _, err := await(bridge, "open", hex.EncodeToString(swapID[:]), paramsJSON); err != nil {
				rejectErr(reject, err)
				return
			}
			onPhase := func(name string) {
				if fn := bridge.Get("phase"); fn.Type() == js.TypeFunction {
					bridge.Call("phase", name)
				}
			}
			err := takerdrive.RunTaker(
				takerdrive.Params{SwapID: swapID, OBXAmount: obxAtomic, XNOAmount: xnoRaw, Fee: p.Fee},
				wasmTakerOBX{bridge}, wasmXNOLocker{bridge}, wasmTransport{bridge}, onPhase,
			)
			if err != nil {
				rejectErr(reject, err)
				return
			}
			resolve.Invoke(map[string]interface{}{"ok": true, "swap_id": hex.EncodeToString(swapID[:])})
		}()
	})
}

// newPromise constructs a JS Promise whose executor runs fn(resolve, reject). The
// executor is invoked synchronously during construction; fn spawns the driver
// goroutine, so the captured resolve/reject settle the Promise later.
func newPromise(fn func(resolve, reject js.Value)) js.Value {
	var handler js.Func
	handler = js.FuncOf(func(_ js.Value, a []js.Value) interface{} {
		fn(a[0], a[1])
		handler.Release()
		return nil
	})
	return js.Global().Get("Promise").New(handler)
}

func rejectErr(reject js.Value, err error) {
	reject.Invoke(js.Global().Get("Error").New(err.Error()))
}
