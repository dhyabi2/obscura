//go:build js && wasm

// Command obscura-wasm is the browser-side Obscura wallet. It compiles the real
// Go wallet/crypto to WebAssembly so a web page can manage keys, scan the chain,
// and build/sign transactions ENTIRELY client-side — keys never leave the browser
// (non-custodial). The page fetches chain data and submits signed txs via the
// node RPC (proxied); this module performs every cryptographic operation locally.
//
// Build: GOOS=js GOARCH=wasm go build -o website/wallet.wasm ./cmd/obscura-wasm
package main

import (
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"syscall/js"

	"obscura/pkg/block"
	"obscura/pkg/commit"
	"obscura/pkg/config"
	"obscura/pkg/mnemonic"
	"obscura/pkg/swapbook"
	"obscura/pkg/tx"
	"obscura/pkg/wallet"
)

// global wallet state (single wallet per page session)
var (
	w    *wallet.Wallet
	seed []byte
)

type jsView struct{ h uint64 }

func (v jsView) Height() uint64 { return v.h }

func errObj(msg string) any { return map[string]any{"error": msg} }

func u64(arg js.Value) (uint64, bool) {
	v, err := strconv.ParseUint(arg.String(), 10, 64)
	return v, err == nil
}

// obxGenerate creates a new wallet from fresh entropy; returns the 24-word
// (256-bit) mnemonic (back this up!) and the address. 256-bit matches Monero's
// seed strength and gives margin for the post-quantum roadmap.
func obxGenerate(this js.Value, args []js.Value) any {
	entropy := make([]byte, 32)
	if _, err := cryptorand.Read(entropy); err != nil {
		return errObj("entropy: " + err.Error())
	}
	phrase, err := mnemonic.Encode(entropy)
	if err != nil {
		return errObj("mnemonic: " + err.Error())
	}
	seed = entropy
	w = wallet.FromSeed(seed)
	return map[string]any{"mnemonic": phrase, "address": w.Address().String()}
}

// obxRestore loads a wallet from a 12-word mnemonic.
func obxRestore(this js.Value, args []js.Value) any {
	if len(args) < 1 {
		return errObj("mnemonic required")
	}
	entropy, err := mnemonic.Decode(args[0].String())
	if err != nil {
		return errObj("bad mnemonic: " + err.Error())
	}
	seed = entropy
	w = wallet.FromSeed(seed)
	return map[string]any{"address": w.Address().String()}
}

func obxInfo(this js.Value, args []js.Value) any {
	if w == nil {
		return errObj("no wallet")
	}
	bal := w.Balance()
	return map[string]any{
		"address":      w.Address().String(),
		"balance":      strconv.FormatUint(bal, 10),
		"balance_obx":  config.FormatAmount(bal),
		"last_scanned": strconv.FormatUint(w.LastScanned(), 10),
		"vault_pub":    hex.EncodeToString(wallet.DeriveVaultKey(seed).Pub),
	}
}

// obxScanBlockHex deserializes a full block (hex) and scans it into the wallet.
func obxScanBlockHex(this js.Value, args []js.Value) any {
	if w == nil {
		return errObj("no wallet")
	}
	raw, err := hex.DecodeString(args[0].String())
	if err != nil {
		return errObj("bad hex")
	}
	b, err := block.DeserializeBlock(raw)
	if err != nil {
		return errObj("bad block: " + err.Error())
	}
	w.ScanBlock(b)
	return map[string]any{"ok": true, "last_scanned": strconv.FormatUint(w.LastScanned(), 10)}
}

// obxExportState returns the wallet's scan state (base64) for localStorage.
func obxExportState(this js.Value, args []js.Value) any {
	if w == nil {
		return errObj("no wallet")
	}
	return map[string]any{"state": base64.StdEncoding.EncodeToString(w.MarshalState())}
}

// obxImportState restores scan state (base64) onto the loaded wallet.
func obxImportState(this js.Value, args []js.Value) any {
	if w == nil {
		return errObj("restore a wallet first")
	}
	raw, err := base64.StdEncoding.DecodeString(args[0].String())
	if err != nil {
		return errObj("bad state")
	}
	if err := w.RestoreState(raw); err != nil {
		return errObj("restore: " + err.Error())
	}
	return map[string]any{"ok": true}
}

// obxBuildSend(dest, amountAtomic, feeAtomic, height) -> {txhex}
func obxBuildSend(this js.Value, args []js.Value) any {
	if w == nil {
		return errObj("no wallet")
	}
	if len(args) < 4 {
		return errObj("need dest, amount, fee, height")
	}
	dest, err := commit.ParseHumanAddress(args[0].String())
	if err != nil {
		return errObj("bad address: " + err.Error())
	}
	amount, ok1 := u64(args[1])
	fee, ok2 := u64(args[2])
	height, ok3 := u64(args[3])
	if !ok1 || !ok2 || !ok3 {
		return errObj("bad numeric arg")
	}
	t, err := w.CreateTransaction(jsView{height}, dest, amount, fee)
	if err != nil {
		return errObj(err.Error())
	}
	return map[string]any{"txhex": hex.EncodeToString(t.Serialize())}
}

// obxBuildVaultDeposit(amount, term, fee, height) -> {txhex, vault_id}
func obxBuildVaultDeposit(this js.Value, args []js.Value) any {
	if w == nil {
		return errObj("no wallet")
	}
	if len(args) < 4 {
		return errObj("need amount, term, fee, height")
	}
	amount, ok1 := u64(args[0])
	term, ok2 := u64(args[1])
	fee, ok3 := u64(args[2])
	height, ok4 := u64(args[3])
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return errObj("bad numeric arg")
	}
	vk := wallet.DeriveVaultKey(seed)
	t, vaultID, err := w.BuildVaultDeposit(jsView{height}, vk.Pub, amount, term, fee)
	if err != nil {
		return errObj(err.Error())
	}
	return map[string]any{"txhex": hex.EncodeToString(t.Serialize()), "vault_id": hex.EncodeToString(vaultID)}
}

// obxBuildVaultClaim(vaultIdHex, principal, term, fee) -> {txhex}
func obxBuildVaultClaim(this js.Value, args []js.Value) any {
	if w == nil {
		return errObj("no wallet")
	}
	if len(args) < 4 {
		return errObj("need vaultId, principal, term, fee")
	}
	vaultID, err := hex.DecodeString(args[0].String())
	if err != nil {
		return errObj("bad vault id")
	}
	principal, ok1 := u64(args[1])
	term, ok2 := u64(args[2])
	fee, ok3 := u64(args[3])
	if !ok1 || !ok2 || !ok3 {
		return errObj("bad numeric arg")
	}
	vk := wallet.DeriveVaultKey(seed)
	t, err := w.BuildVaultClaim(vk, vaultID, principal, term, fee)
	if err != nil {
		return errObj(err.Error())
	}
	return map[string]any{"txhex": hex.EncodeToString(t.Serialize())}
}

// obxBuildOffer(giveAsset, getAsset, giveAmt, getAmt, expiryUnix) -> {offerhex}
// Signs a swap order-book offer with a maker key derived from the seed.
func obxBuildOffer(this js.Value, args []js.Value) any {
	if w == nil {
		return errObj("no wallet")
	}
	if len(args) < 5 {
		return errObj("need give, get, giveAmt, getAmt, expiry")
	}
	giveAmt, ok1 := u64(args[2])
	getAmt, ok2 := u64(args[3])
	expiry, ok3 := u64(args[4])
	if !ok1 || !ok2 || !ok3 {
		return errObj("bad numeric arg")
	}
	o := &swapbook.Offer{
		GiveAsset:  args[0].String(),
		GetAsset:   args[1].String(),
		GiveAmount: giveAmt,
		GetAmount:  getAmt,
		Expiry:     int64(expiry),
	}
	makerSecret := commit.HashToScalar([]byte("Obscura/maker/v1"), seed)
	o.Sign(makerSecret)
	return map[string]any{"offerhex": hex.EncodeToString(o.Serialize())}
}

// obxParseOffer(hex) -> decoded order-book offer fields (for display).
func obxParseOffer(this js.Value, args []js.Value) any {
	if len(args) < 1 {
		return errObj("offer hex required")
	}
	raw, err := hex.DecodeString(args[0].String())
	if err != nil {
		return errObj("bad hex")
	}
	o, err := swapbook.ParseOffer(raw)
	if err != nil {
		return errObj("bad offer: " + err.Error())
	}
	id := o.ID()
	return map[string]any{
		"id":          hex.EncodeToString(id[:]),
		"maker":       hex.EncodeToString(o.Maker),
		"give_asset":  o.GiveAsset,
		"get_asset":   o.GetAsset,
		"give_amount": strconv.FormatUint(o.GiveAmount, 10),
		"get_amount":  strconv.FormatUint(o.GetAmount, 10),
		"expiry":      strconv.FormatInt(o.Expiry, 10),
	}
}

// obxReleaseReservation releases the local input reservations a previously-built (but not
// successfully broadcast) transaction made, so those inputs become spendable again. The page
// MUST call this when a submit/broadcast fails (audit #20: otherwise the reserved outputs are
// stuck for the session and the next send fails with a spurious "insufficient funds").
func obxReleaseReservation(this js.Value, args []js.Value) any {
	if w == nil {
		return errObj("no wallet")
	}
	if len(args) < 1 {
		return errObj("need txhex")
	}
	raw, err := hex.DecodeString(args[0].String())
	if err != nil {
		return errObj("bad txhex: " + err.Error())
	}
	t, err := tx.Deserialize(raw)
	if err != nil {
		return errObj("bad tx: " + err.Error())
	}
	w.ReleaseReservation(t)
	return map[string]any{"ok": true}
}

// obxScanUndo rolls back the wallet's scan state to before `fromHeight` so the page can
// recover from a chain reorg (audit #19): it un-spends outputs whose spend was orphaned and
// drops outputs received in orphaned blocks. The page MUST call this with the fork height when
// it detects that the node's block at a previously-scanned height changed, THEN re-scan the new
// branch forward from fromHeight. Without it, a reorg leaves the balance wrong (funds stuck).
func obxScanUndo(this js.Value, args []js.Value) any {
	if w == nil {
		return errObj("no wallet")
	}
	if len(args) < 1 {
		return errObj("need fromHeight")
	}
	h, ok := u64(args[0])
	if !ok {
		return errObj("bad fromHeight")
	}
	w.ScanBlockUndo(h)
	return map[string]any{"ok": true}
}

func main() {
	js.Global().Set("obxParseOffer", js.FuncOf(obxParseOffer))
	js.Global().Set("obxReleaseReservation", js.FuncOf(obxReleaseReservation))
	js.Global().Set("obxScanUndo", js.FuncOf(obxScanUndo))
	js.Global().Set("obxGenerate", js.FuncOf(obxGenerate))
	js.Global().Set("obxRestore", js.FuncOf(obxRestore))
	js.Global().Set("obxInfo", js.FuncOf(obxInfo))
	js.Global().Set("obxScanBlockHex", js.FuncOf(obxScanBlockHex))
	js.Global().Set("obxExportState", js.FuncOf(obxExportState))
	js.Global().Set("obxImportState", js.FuncOf(obxImportState))
	js.Global().Set("obxBuildSend", js.FuncOf(obxBuildSend))
	js.Global().Set("obxBuildVaultDeposit", js.FuncOf(obxBuildVaultDeposit))
	js.Global().Set("obxBuildVaultClaim", js.FuncOf(obxBuildVaultClaim))
	js.Global().Set("obxBuildOffer", js.FuncOf(obxBuildOffer))
	js.Global().Set("obxReady", js.ValueOf(true))
	select {} // keep the Go runtime alive for callbacks
}
