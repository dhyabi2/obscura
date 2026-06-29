// Command obscura-testwallet manages a LOCAL test wallet for swap/DEX testing
// (.testwallet/testwallet.json). The XNO account is real Nano mainnet (fund a TINY
// amount — 0.00001 XNO max); the OBX side is a seed self-funded by mining on the
// value-less test chain. Secrets are local files for tiny test amounts only.
//
//	obscura-testwallet                      generate a fresh test wallet
//	obscura-testwallet addr                 print the XNO address + saved file
//	obscura-testwallet send-raw <raw> <dst> send <raw> XNO from the wallet to <dst>
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"

	"filippo.io/edwards25519"

	"obscura/pkg/commit"
	"obscura/pkg/swapd"
)

const walletPath = "/Users/mac/XMR_alternative/.testwallet/testwallet.json"

type testWallet struct {
	XNOAddress   string `json:"xno_address"`
	XNOSecretHex string `json:"xno_secret_hex"`
	OBXSeedHex   string `json:"obx_seed_hex"`
	Note         string `json:"note"`
}

func load() testWallet {
	b, err := os.ReadFile(walletPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "no test wallet — run `obscura-testwallet` to generate:", err)
		os.Exit(1)
	}
	var w testWallet
	if err := json.Unmarshal(b, &w); err != nil {
		fmt.Fprintln(os.Stderr, "bad wallet file:", err)
		os.Exit(1)
	}
	return w
}

func secret(w testWallet) *edwards25519.Scalar {
	raw, err := hex.DecodeString(w.XNOSecretHex)
	if err != nil || len(raw) != 32 {
		fmt.Fprintln(os.Stderr, "bad xno secret")
		os.Exit(1)
	}
	s, err := new(edwards25519.Scalar).SetCanonicalBytes(raw)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bad xno scalar:", err)
		os.Exit(1)
	}
	return s
}

func nano() *swapd.NanoRPC {
	cfg, _ := swapd.ResolveNanoSelector("rainstorm")
	n, err := swapd.NewNanoRPC(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nano rpc:", err)
		os.Exit(1)
	}
	return n
}

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "addr":
			w := load()
			fmt.Printf("XNO: %s\nfile: %s\n", w.XNOAddress, walletPath)
			return
		case "send-raw":
			if len(os.Args) != 4 {
				fmt.Fprintln(os.Stderr, "usage: obscura-testwallet send-raw <amountRaw> <destAddr>")
				os.Exit(2)
			}
			amt, ok := new(big.Int).SetString(os.Args[2], 10)
			if !ok || amt.Sign() <= 0 {
				fmt.Fprintln(os.Stderr, "amountRaw must be a positive integer (raw)")
				os.Exit(2)
			}
			// Safety cap: refuse to send more than 0.00001 XNO (1e25 raw) from this tool.
			cap1e25, _ := new(big.Int).SetString("10000000000000000000000000", 10)
			if amt.Cmp(cap1e25) > 0 {
				fmt.Fprintf(os.Stderr, "refusing: %s raw exceeds the 0.00001 XNO test cap (1e25 raw)\n", amt)
				os.Exit(2)
			}
			w := load()
			fmt.Printf("sending %s raw XNO -> %s …\n", amt, os.Args[3])
			if err := nano().Send(secret(w), amt, os.Args[3]); err != nil {
				fmt.Fprintln(os.Stderr, "send failed:", err)
				os.Exit(1)
			}
			fmt.Println("sent ✓")
			return
		}
	}

	// default: generate a fresh wallet.
	xsk := commit.RandomScalar()
	xpub := new(edwards25519.Point).ScalarBaseMult(xsk).Bytes()
	xaddr, err := swapd.EncodeNanoAddress(xpub)
	if err != nil {
		fmt.Fprintln(os.Stderr, "encode nano address:", err)
		os.Exit(1)
	}
	obxSeed := make([]byte, 32)
	if _, err := rand.Read(obxSeed); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
	out := testWallet{
		XNOAddress:   xaddr,
		XNOSecretHex: hex.EncodeToString(xsk.Bytes()),
		OBXSeedHex:   hex.EncodeToString(obxSeed),
		Note:         "TEST wallet. XNO = real Nano mainnet, fund 0.00001 MAX. OBX = self-mined on the value-less test chain. Keep xno_secret_hex to recover funds.",
	}
	os.MkdirAll(filepath.Dir(walletPath), 0o700)
	b, _ := json.MarshalIndent(out, "", "  ")
	if err := os.WriteFile(walletPath, b, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "write:", err)
		os.Exit(1)
	}
	fmt.Println("=== Obscura test wallet generated ===")
	fmt.Printf("XNO address to FUND (0.00001 XNO max): %s\n", xaddr)
	fmt.Printf("saved (0600): %s\n", walletPath)
}
