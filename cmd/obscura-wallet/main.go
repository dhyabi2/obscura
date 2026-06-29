// Command obscura-wallet is the Obscura CLI wallet: create keys, check balance,
// and send confidential transactions through a node's RPC.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"obscura/pkg/block"
	"obscura/pkg/commit"
	"obscura/pkg/config"
	"obscura/pkg/keystore"
	"obscura/pkg/mnemonic"
	"obscura/pkg/rpc"
	"obscura/pkg/swapbook"
	"obscura/pkg/tx"
	"obscura/pkg/uri"
	"obscura/pkg/wallet"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	cmd := os.Args[1]
	args := flag.NewFlagSet(cmd, flag.ExitOnError)
	walletPath := args.String("wallet", defaultWalletPath(), "wallet seed file")
	node := args.String("node", fmt.Sprintf("http://127.0.0.1:%d", config.DefaultRPCPort), "node RPC URL")
	to := args.String("to", "", "recipient address (hex)")
	amount := args.String("amount", "", "amount in OBX (e.g. 1.5)")
	fee := args.String("fee", "0.0001", "fee in OBX")
	txid := args.String("txid", "", "transaction id (bump)")
	passphrase := args.String("passphrase", "", "wallet passphrase (or set OBSCURA_WALLET_PASSPHRASE); enables at-rest encryption on `new`")
	newPass := args.String("new-passphrase", "", "new passphrase (or set OBSCURA_WALLET_NEW_PASSPHRASE) for `passwd`")
	mnemonicArg := args.String("mnemonic", "", "seed word phrase (restore)")
	proofArg := args.String("proof", "", "hex receipt proof (checkproof)")
	addrArg := args.String("address", "", "address to verify against (checkproof)")
	viewkeyArg := args.String("viewkey", "", "64-byte hex view key (watch)")
	labelArg := args.String("label", "", "label for a payment URI (uri)")
	maturity := args.Uint64("coinbase-maturity", config.CoinbaseMaturity, "network coinbase maturity (must match the node/network)")
	giveAsset := args.String("give-asset", "OBX", "asset you offer to give (offer)")
	getAsset := args.String("get-asset", "XNO", "asset you want in return (offer)")
	giveAmt := args.Uint64("give-amount", 0, "amount you give, atomic units (offer)")
	getAmt := args.Uint64("get-amount", 0, "amount you want, atomic units (offer)")
	args.Parse(os.Args[2:])
	config.CoinbaseMaturity = *maturity
	optPassphrase = *passphrase

	switch cmd {
	case "new":
		cmdNew(*walletPath)
	case "mnemonic":
		cmdMnemonic(*walletPath)
	case "restore":
		cmdRestore(*walletPath, *mnemonicArg)
	case "address":
		cmdAddress(*walletPath)
	case "subaddress":
		cmdSubaddress(*walletPath)
	case "uri":
		cmdURI(*walletPath, *amount, *labelArg)
	case "viewkey":
		cmdViewKey(*walletPath)
	case "watch":
		cmdWatch(*walletPath, *viewkeyArg)
	case "balance":
		cmdBalance(*walletPath, *node)
	case "outputs":
		cmdOutputs(*walletPath, *node)
	case "send":
		cmdSend(*walletPath, *node, *to, *amount, *fee)
	case "sweep":
		cmdSweep(*walletPath, *node, *to, *fee)
	case "passwd":
		cmdPasswd(*walletPath, *newPass)
	case "history":
		cmdHistory(*walletPath, *node)
	case "bump":
		cmdBump(*walletPath, *node, *txid, *fee)
	case "proof":
		cmdProof(*walletPath, *node, *txid)
	case "proofsent":
		cmdProofSent(*walletPath, *node, *txid)
	case "checkproof":
		cmdCheckProof(*addrArg, *proofArg)
	case "status":
		cmdStatus(*node)
	case "feerate":
		cmdFeeRate(*node)
	case "mempool":
		cmdMempool(*node)
	case "peers":
		cmdPeers(*node)
	case "offers":
		cmdOffers(*node)
	case "offer":
		cmdOffer(*walletPath, *node, *giveAsset, *getAsset, *giveAmt, *getAmt)
	case "zkmint":
		cmdZKMint(*walletPath, *node, args.Arg(0), *fee)
	case "zklist":
		cmdZKList(*walletPath, *node)
	case "zkspend":
		cmdZKSpend(*walletPath, *node, args.Arg(0), args.Arg(1), *fee)
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `obscura-wallet — %s (%s) CLI wallet

Usage:
  obscura-wallet new        [--wallet FILE] [--passphrase PASS]   (encrypts the seed at rest)
  obscura-wallet mnemonic   [--wallet FILE] [--passphrase PASS]   (print word-phrase backup)
  obscura-wallet restore    [--wallet FILE] --mnemonic "words..." [--passphrase PASS]
  obscura-wallet passwd     [--wallet FILE] [--passphrase OLD] --new-passphrase NEW
  obscura-wallet address    [--wallet FILE]
  obscura-wallet subaddress [--wallet FILE]                             (fresh unlinkable address)
  obscura-wallet uri        [--wallet FILE] [--amount OBX] [--label L]  (payment URI / QR)
  obscura-wallet viewkey    [--wallet FILE]                             (export view key for watching)
  obscura-wallet watch      --wallet FILE --viewkey HEX                 (create a watch-only wallet)
  obscura-wallet balance    [--wallet FILE] [--node URL]
  obscura-wallet outputs    [--wallet FILE] [--node URL]                (list unspent outputs)
  obscura-wallet send       [--wallet FILE] [--node URL] --to HEXADDR --amount OBX [--fee OBX|auto]
  obscura-wallet sweep      [--wallet FILE] [--node URL] --to HEXADDR [--fee OBX]   (send entire balance)
  obscura-wallet history    [--wallet FILE] [--node URL]
  obscura-wallet bump       [--wallet FILE] [--node URL] --txid TXID --fee OBX
  obscura-wallet proof      [--wallet FILE] [--node URL] --txid TXID    (prove you received it)
  obscura-wallet proofsent  [--wallet FILE] [--node URL] --txid TXID    (prove you PAID it)
  obscura-wallet checkproof --address ADDR --proof HEX                  (verify a receipt proof)
  obscura-wallet status     [--node URL]
  obscura-wallet feerate    [--node URL]
  obscura-wallet mempool    [--node URL]
  obscura-wallet peers      [--node URL]
  obscura-wallet offers     [--node URL]
  obscura-wallet offer      [--wallet FILE] [--node URL] --give-asset A --give-amount N --get-asset B --get-amount M
  obscura-wallet zkmint     [--wallet FILE] [--node URL] [--fee OBX] <amount>            (shield value into a ZK note, UNAUDITED)
  obscura-wallet zklist     [--wallet FILE] [--node URL]                                 (list spendable ZK notes)
  obscura-wallet zkspend    [--wallet FILE] [--node URL] [--fee OBX] <coinIndex> <to>    (spend a ZK note anonymously, UNAUDITED)
`, config.CoinName, config.Ticker)
}

func cmdNew(path string) {
	seed := make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		fmt.Println("error: rng failure:", err)
		os.Exit(1)
	}
	createSeedFile(path, seed)
}

// createSeedFile writes a NEW wallet seed file (refusing to clobber an existing
// one), encrypting it when a passphrase is available. Shared by `new` and
// `restore`.
func createSeedFile(path string, seed []byte) {
	if _, err := os.Stat(path); err == nil {
		fmt.Printf("wallet already exists at %s\n", path)
		os.Exit(1)
	}
	os.MkdirAll(filepath.Dir(path), 0700)

	// if a passphrase is supplied, store the seed ENCRYPTED at rest; otherwise
	// store legacy plaintext hex (with a loud warning).
	var payload []byte
	encrypted := false
	if pass := getPassphrase(); len(pass) > 0 {
		blob, err := keystore.Encrypt(seed, pass)
		if err != nil {
			fmt.Println("error: encrypt:", err)
			os.Exit(1)
		}
		payload = blob
		encrypted = true
	} else {
		payload = []byte(hex.EncodeToString(seed))
	}

	// create exclusively with 0600 so we never clobber an existing seed.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}
	if _, err := f.Write(payload); err != nil {
		f.Close()
		fmt.Println("error:", err)
		os.Exit(1)
	}
	f.Close()
	w := wallet.FromSeed(seed)
	fmt.Printf("Created wallet: %s\n", path)
	fmt.Printf("Address: %s\n", w.Address().String())
	if encrypted {
		fmt.Println("\nWallet is ENCRYPTED. Back up the seed file AND remember your passphrase —")
		fmt.Println("there is no recovery if you lose either.")
	} else {
		fmt.Println("\nWARNING: seed stored in PLAINTEXT. Re-create with --passphrase (or set")
		fmt.Println("OBSCURA_WALLET_PASSPHRASE) to encrypt it. Anyone who reads this file owns your funds.")
	}
	fmt.Println("\nTip: run `obscura-wallet mnemonic` to get a word-phrase backup of this seed.")
}

// cmdMnemonic prints the seed as a checksummed word phrase for backup.
func cmdMnemonic(path string) {
	seed := loadSeed(path)
	phrase, err := mnemonic.Encode(seed)
	if err != nil {
		fmt.Println("mnemonic:", err)
		os.Exit(1)
	}
	fmt.Println("Seed phrase — write it down and keep it SECRET (anyone with it owns your funds):")
	fmt.Println()
	fmt.Println(" ", phrase)
}

// cmdRestore recreates a wallet seed file from a word phrase.
func cmdRestore(path, phrase string) {
	if phrase == "" {
		phrase = os.Getenv("OBSCURA_WALLET_MNEMONIC")
	}
	if phrase == "" {
		fmt.Println("--mnemonic (or OBSCURA_WALLET_MNEMONIC) is required")
		os.Exit(1)
	}
	seed, err := mnemonic.Decode(phrase)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	createSeedFile(path, seed)
}

// optPassphrase is the --passphrase flag value; getPassphrase falls back to the
// environment so the passphrase need not appear in shell history / process args.
var optPassphrase string

func getPassphrase() []byte {
	if optPassphrase != "" {
		return []byte(optPassphrase)
	}
	if env := os.Getenv("OBSCURA_WALLET_PASSPHRASE"); env != "" {
		return []byte(env)
	}
	return nil
}

func loadSeed(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Printf("no wallet at %s (run `obscura-wallet new`)\n", path)
		os.Exit(1)
	}
	if keystore.IsEncrypted(data) {
		pass := getPassphrase()
		if len(pass) == 0 {
			fmt.Println("wallet is encrypted — set OBSCURA_WALLET_PASSPHRASE or pass --passphrase")
			os.Exit(1)
		}
		seed, err := keystore.Decrypt(data, pass)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		return seed
	}
	seed, err := hex.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		fmt.Println("corrupt wallet seed")
		os.Exit(1)
	}
	return seed
}

// watchPrefix marks a watch-only wallet file (stores a view key, not a seed).
const watchPrefix = "OBXVIEW1:"

func loadWallet(path string) *wallet.Wallet {
	if data, err := os.ReadFile(path); err == nil {
		s := strings.TrimSpace(string(data))
		if strings.HasPrefix(s, watchPrefix) {
			vk, err := hex.DecodeString(strings.TrimPrefix(s, watchPrefix))
			if err != nil {
				fmt.Println("corrupt watch wallet")
				os.Exit(1)
			}
			w, err := wallet.FromViewKey(vk)
			if err != nil {
				fmt.Println("bad view key:", err)
				os.Exit(1)
			}
			return w
		}
	}
	return wallet.FromSeed(loadSeed(path))
}

func cmdAddress(path string) {
	w := loadWallet(path)
	fmt.Println(w.Address().String())
}

// cmdViewKey exports this wallet's view key. Sharing it lets someone watch
// incoming payments and read amounts but NOT spend.
func cmdViewKey(path string) {
	w := loadWallet(path)
	fmt.Println("View key (lets the holder SEE incoming payments but not spend):")
	fmt.Println(hex.EncodeToString(w.ViewKey()))
}

// cmdWatch creates a watch-only wallet file from a view key.
func cmdWatch(path, viewkeyHex string) {
	if viewkeyHex == "" {
		fmt.Println("--viewkey HEX is required")
		os.Exit(1)
	}
	if _, err := os.Stat(path); err == nil {
		fmt.Printf("wallet already exists at %s\n", path)
		os.Exit(1)
	}
	vk, err := hex.DecodeString(strings.TrimSpace(viewkeyHex))
	if err != nil {
		fmt.Println("bad view key hex")
		os.Exit(1)
	}
	w, err := wallet.FromViewKey(vk)
	if err != nil {
		fmt.Println("bad view key:", err)
		os.Exit(1)
	}
	os.MkdirAll(filepath.Dir(path), 0700)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}
	if _, err := f.WriteString(watchPrefix + hex.EncodeToString(vk)); err != nil {
		f.Close()
		fmt.Println("error:", err)
		os.Exit(1)
	}
	f.Close()
	fmt.Printf("Created WATCH-ONLY wallet: %s\n", path)
	fmt.Printf("Address: %s\n", w.Address().String())
	fmt.Println("This wallet can see incoming payments but cannot spend.")
}

// cmdSubaddress generates a fresh, unlinkable receiving subaddress (a sub-account
// of the same seed) and persists the new count so it will be scanned thereafter.
func cmdSubaddress(path string) {
	w := loadWallet(path)
	loadState(w, statePathFor(path))
	idx, addr := w.NewSubaddress()
	saveState(w, statePathFor(path))
	fmt.Printf("Subaddress #%d:\n%s\n", idx, addr.String())
}

// cmdURI prints a BIP21-style payment URI (for QR codes / pay links).
func cmdURI(path, amount, label string) {
	w := loadWallet(path)
	fmt.Println(uri.Format(w.Address().String(), amount, label))
}

// parseAddressInput accepts an obscura: payment URI, a Base58 checksummed
// address, or a raw 128-char hex address (back-compat).
func parseAddressInput(s string) (commit.StealthAddress, error) {
	if uri.IsURI(s) {
		addr, _, _, err := uri.Parse(s)
		if err != nil {
			return commit.StealthAddress{}, err
		}
		s = addr
	}
	if a, err := commit.ParseHumanAddress(s); err == nil {
		return a, nil
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return commit.StealthAddress{}, fmt.Errorf("address is neither a valid Base58 address nor hex")
	}
	return commit.DecodeAddress(b)
}

func cmdBalance(path, nodeURL string) {
	w := loadWallet(path)
	scan(w, nodeURL, statePathFor(path))
	fmt.Printf("Balance: %s %s\n", config.FormatAmount(w.Balance()), config.Ticker)
	unspent := 0
	for _, o := range w.Outputs {
		if !o.Spent {
			unspent++
		}
	}
	fmt.Printf("Spendable outputs: %d\n", unspent)
}

// cmdOutputs lists the wallet's unspent outputs with their maturity status.
func cmdOutputs(path, nodeURL string) {
	w := loadWallet(path)
	scan(w, nodeURL, statePathFor(path))
	client, err := rpc.NewClient(nodeURL)
	if err != nil {
		fmt.Println("rpc:", err)
		os.Exit(1)
	}
	height := client.Height()
	spendable := w.SpendableOutputs(height + 1)
	spendableSet := make(map[string]bool, len(spendable))
	for _, o := range spendable {
		spendableSet[string(o.Out.OneTimeKey)] = true
	}
	n := 0
	for _, o := range w.Outputs {
		if o.Spent {
			continue
		}
		n++
		status := "spendable"
		if !spendableSet[string(o.Out.OneTimeKey)] {
			status = "locked/immature"
		}
		fmt.Printf("  %s %s  (height %d%s) [%s]\n",
			config.FormatAmount(o.Amount), config.Ticker, o.Height,
			map[bool]string{true: " coinbase", false: ""}[o.IsCoinbase], status)
	}
	if n == 0 {
		fmt.Println("no unspent outputs")
		return
	}
	fmt.Printf("%d unspent output(s), total %s %s\n", n, config.FormatAmount(w.Balance()), config.Ticker)
}

func cmdSend(path, nodeURL, to, amountStr, feeStr string) {
	if to == "" || amountStr == "" {
		fmt.Println("--to and --amount are required")
		os.Exit(1)
	}
	w := loadWallet(path)
	client, err := rpc.NewClient(nodeURL)
	if err != nil {
		fmt.Println("rpc:", err)
		os.Exit(1)
	}
	scan(w, nodeURL, statePathFor(path))

	dest, err := parseAddressInput(to)
	if err != nil {
		fmt.Println("bad address:", err)
		os.Exit(1)
	}
	amount := parseOBX(amountStr)
	var fee uint64
	if feeStr == "auto" {
		fee = autoFee(client, w, dest, amount)
		fmt.Printf("Auto fee: %s %s\n", config.FormatAmount(fee), config.Ticker)
	} else {
		fee = parseOBX(feeStr)
	}

	t, err := w.CreateTransaction(client, dest, amount, fee)
	if err != nil {
		fmt.Println("create tx:", err)
		os.Exit(1)
	}
	txid, err := client.SubmitTx(t)
	if err != nil {
		fmt.Println("submit:", err)
		os.Exit(1)
	}
	// record the outgoing payment so `history` and `bump` can find it
	w.RecordSent(t, dest, amount)
	saveState(w, statePathFor(path))
	fmt.Printf("Sent %s %s (fee %s) — txid %s\n",
		config.FormatAmount(amount), config.Ticker, config.FormatAmount(fee), txid)
}

// cmdSweep sends the wallet's entire spendable balance to one address.
func cmdSweep(path, nodeURL, to, feeStr string) {
	if to == "" {
		fmt.Println("--to is required")
		os.Exit(1)
	}
	w := loadWallet(path)
	client, err := rpc.NewClient(nodeURL)
	if err != nil {
		fmt.Println("rpc:", err)
		os.Exit(1)
	}
	scan(w, nodeURL, statePathFor(path))
	dest, err := parseAddressInput(to)
	if err != nil {
		fmt.Println("bad address:", err)
		os.Exit(1)
	}
	fee := parseOBX(feeStr)
	t, err := w.CreateSweepTransaction(client, dest, fee)
	if err != nil {
		fmt.Println("sweep:", err)
		os.Exit(1)
	}
	txid, err := client.SubmitTx(t)
	if err != nil {
		fmt.Println("submit:", err)
		os.Exit(1)
	}
	w.RecordSent(t, dest, w.Balance())
	saveState(w, statePathFor(path))
	fmt.Printf("Swept balance to %s… (fee %s %s) — txid %s\n",
		hex.EncodeToString(dest.Encode())[:12], config.FormatAmount(fee), config.Ticker, txid)
}

// cmdZKMint shields transparent value into a ZK note owned by THIS wallet, broadcasts
// the mint, and persists the note so it can later be spent anonymously. WARNING: the
// anonymous-spend path is UNAUDITED, do not secure real value with it.
func cmdZKMint(path, nodeURL, amountStr, feeStr string) {
	if amountStr == "" {
		fmt.Println("usage: obscura-wallet zkmint [--fee OBX] <amount>")
		os.Exit(1)
	}
	w := loadWallet(path)
	if w.IsViewOnly() {
		fmt.Println("a view-only wallet cannot mint")
		os.Exit(1)
	}
	client, err := rpc.NewClient(nodeURL)
	if err != nil {
		fmt.Println("rpc:", err)
		os.Exit(1)
	}
	scan(w, nodeURL, statePathFor(path))

	amount := parseOBX(amountStr)
	fee := parseOBX(feeStr)
	t, coin, err := w.CreateZKMint(client, amount, fee)
	if err != nil {
		fmt.Println("create mint:", err)
		os.Exit(1)
	}
	txid, err := client.SubmitTx(t)
	if err != nil {
		fmt.Println("submit:", err)
		os.Exit(1)
	}
	// the minted note is owned by THIS wallet, so persist it for spending later.
	w.AddZKCoin(coin)
	saveState(w, statePathFor(path))
	fmt.Printf("Minted ZK note: %s %s (fee %s), txid %s\n",
		config.FormatAmount(amount), config.Ticker, config.FormatAmount(fee), txid)
	fmt.Printf("  leaf (commitment): %s\n", hex.EncodeToString(coin.Leaf))
	fmt.Println("NOTE: the note must be confirmed in a block before it can be spent (zkspend).")
	fmt.Println("WARNING: the anonymous-spend path is UNAUDITED, do not secure real value with it.")
}

// cmdZKList lists the wallet's spendable ZK notes (minted-to-self or received via a
// shielded transfer), refreshing from the chain first so received notes are captured.
func cmdZKList(path, nodeURL string) {
	w := loadWallet(path)
	scan(w, nodeURL, statePathFor(path))
	coins := w.ZKCoins()
	if len(coins) == 0 {
		fmt.Println("no ZK notes")
		return
	}
	fmt.Printf("%d ZK note(s):\n", len(coins))
	for i, c := range coins {
		fmt.Printf("  [%d] %s %s  leaf %s\n",
			i, config.FormatAmount(c.Amount), config.Ticker, hex.EncodeToString(c.Leaf))
	}
}

// cmdZKSpend spends a ZK note anonymously to a destination address: it fetches the
// note's membership witness (anchor + path) from the node, builds the spend proof
// offline, and broadcasts it. WARNING: this anonymous-spend path is UNAUDITED.
func cmdZKSpend(path, nodeURL, indexStr, to, feeStr string) {
	if indexStr == "" || to == "" {
		fmt.Println("usage: obscura-wallet zkspend [--fee OBX] <coinIndex> <to>")
		os.Exit(1)
	}
	w := loadWallet(path)
	if w.IsViewOnly() {
		fmt.Println("a view-only wallet cannot spend")
		os.Exit(1)
	}
	client, err := rpc.NewClient(nodeURL)
	if err != nil {
		fmt.Println("rpc:", err)
		os.Exit(1)
	}
	scan(w, nodeURL, statePathFor(path))

	idx, err := strconv.Atoi(indexStr)
	if err != nil {
		fmt.Println("bad coin index:", err)
		os.Exit(1)
	}
	coins := w.ZKCoins()
	if idx < 0 || idx >= len(coins) {
		fmt.Printf("coin index %d out of range (have %d ZK note(s); see zklist)\n", idx, len(coins))
		os.Exit(1)
	}
	coin := coins[idx]
	dest, err := parseAddressInput(to)
	if err != nil {
		fmt.Println("bad address:", err)
		os.Exit(1)
	}
	fee := parseOBX(feeStr)

	// fetch the membership witness (anchor + authentication path) from the node. The
	// note must already be CONFIRMED in a block for the node to know its leaf.
	anchor, mpath, depth, ok := client.ZKWitnessFor(coin.Leaf)
	if !ok {
		fmt.Println("witness unavailable: the ZK note is not yet confirmed on-chain (mint it, wait for a block, then retry).")
		os.Exit(1)
	}
	t, err := w.CreateZKSpend(coin, anchor, mpath, depth, dest, fee)
	if err != nil {
		fmt.Println("create spend:", err)
		os.Exit(1)
	}
	txid, err := client.SubmitTx(t)
	if err != nil {
		fmt.Println("submit:", err)
		os.Exit(1)
	}
	// record the spend so it shows in `history` and can be fee-bumped (`bump`) if it
	// sticks: the bump re-spends the SAME coin (same nullifier) at a higher fee.
	w.RecordSent(t, dest, coin.Amount-fee)
	saveState(w, statePathFor(path))
	fmt.Printf("Spent ZK note [%d] anonymously: %s %s (fee %s) to %s..., txid %s\n",
		idx, config.FormatAmount(coin.Amount-fee), config.Ticker, config.FormatAmount(fee),
		hex.EncodeToString(dest.Encode())[:12], txid)
	fmt.Println("WARNING: the anonymous-spend path is UNAUDITED, do not secure real value with it.")
}

// cmdHistory lists outgoing payments this wallet has recorded, refreshing their
// confirmation status by scanning new blocks first.
func cmdHistory(path, nodeURL string) {
	w := loadWallet(path)
	scan(w, nodeURL, statePathFor(path))
	hist := w.SentHistory()
	if len(hist) == 0 {
		fmt.Println("no outgoing payments recorded")
		return
	}
	fmt.Printf("%d outgoing payment(s):\n", len(hist))
	for _, s := range hist {
		status := "PENDING"
		if s.Replaced {
			status = "replaced"
		} else if s.Height > 0 {
			status = fmt.Sprintf("confirmed @ %d", s.Height)
		}
		fmt.Printf("  %s…  %s %s  fee %s  → %s…  [%s]\n",
			s.TxID[:12], config.FormatAmount(s.Amount), config.Ticker,
			config.FormatAmount(s.Fee), hex.EncodeToString(s.Dest)[:12], status)
	}
}

// cmdBump replaces a stuck outgoing payment with a higher-fee version that
// re-spends the same inputs (replace-by-fee).
func cmdBump(path, nodeURL, txid, feeStr string) {
	if txid == "" {
		fmt.Println("--txid is required")
		os.Exit(1)
	}
	w := loadWallet(path)
	scan(w, nodeURL, statePathFor(path))
	s := w.FindSent(txid)
	if s == nil {
		fmt.Println("no recorded payment with that txid (run `history`)")
		os.Exit(1)
	}
	if s.Height > 0 {
		fmt.Println("that payment is already confirmed — nothing to bump")
		os.Exit(1)
	}
	if s.Replaced {
		fmt.Println("that payment was already replaced by a bump")
		os.Exit(1)
	}
	prev, err := tx.Deserialize(s.Raw)
	if err != nil {
		fmt.Println("corrupt stored tx:", err)
		os.Exit(1)
	}
	dest, err := commit.DecodeAddress(s.Dest)
	if err != nil {
		fmt.Println("corrupt stored address:", err)
		os.Exit(1)
	}
	newFee := parseOBX(feeStr)
	client, err := rpc.NewClient(nodeURL)
	if err != nil {
		fmt.Println("rpc:", err)
		os.Exit(1)
	}

	// Pick the bump path. Anonymous (ZKInputs) and confidential (CZKSpends) spends are
	// rebuilt from the SAME coin at a higher fee — the nullifier is unchanged, so it is
	// a genuine RBF replacement, not a second spend. Everything else is a transparent
	// re-spend of the same inputs. (A ZK *mint* has transparent Inputs, so it correctly
	// falls to the transparent path.)
	var t *tx.Transaction
	var recordAmount uint64
	if len(prev.ZKInputs) > 0 || len(prev.CZKSpends) > 0 {
		var nf []byte
		if len(prev.CZKSpends) > 0 {
			nf = prev.CZKSpends[0].Nullifier
		} else {
			nf = prev.ZKInputs[0].Nullifier
		}
		coin := w.ZKCoinForNullifier(nf)
		if coin == nil {
			fmt.Println("bump: the spent ZK note is no longer known to this wallet (cannot rebuild)")
			os.Exit(1)
		}
		anchor, mpath, depth, ok := client.ZKWitnessFor(coin.Leaf)
		if !ok {
			fmt.Println("bump: membership witness unavailable (the ZK note is no longer confirmed/known on-chain)")
			os.Exit(1)
		}
		t, err = w.BumpZKSpend(prev, coin, anchor, mpath, depth, dest, newFee)
		if err != nil {
			fmt.Println("bump:", err)
			os.Exit(1)
		}
		recordAmount = coin.Amount - newFee
	} else {
		t, err = w.BumpFee(prev, dest, s.Amount, newFee)
		if err != nil {
			fmt.Println("bump:", err)
			os.Exit(1)
		}
		recordAmount = s.Amount
	}

	newID, err := client.SubmitTx(t)
	if err != nil {
		fmt.Println("submit:", err)
		os.Exit(1)
	}
	s.Replaced = true
	w.RecordSent(t, dest, recordAmount)
	saveState(w, statePathFor(path))
	fmt.Printf("Bumped %s… → %s (fee %s %s)\n",
		txid[:12], newID, config.FormatAmount(newFee), config.Ticker)
}

func cmdStatus(nodeURL string) {
	client, err := rpc.NewClient(nodeURL)
	if err != nil {
		fmt.Println("rpc:", err)
		os.Exit(1)
	}
	st, err := client.Status()
	if err != nil {
		fmt.Println("status:", err)
		os.Exit(1)
	}
	fmt.Printf("%s (%s)\n", st.Coin, st.Ticker)
	fmt.Printf("  height:           %d\n", st.Height)
	fmt.Printf("  difficulty:       %d\n", st.Difficulty)
	fmt.Printf("  supply emitted:   %s %s\n", st.EmittedOBX, st.Ticker)
	fmt.Printf("  incentive pool:   %s %s\n", config.FormatAmount(st.IncentivePool), st.Ticker)
	fmt.Printf("  anonymity set:    %d outputs\n", st.AccSize)
	fmt.Printf("  accumulator:      %s\n", st.Backend)
	fmt.Printf("  proof-of-work:    %s\n", st.PoWBackend)
	fmt.Printf("  mempool:          %d txs\n", st.MempoolSize)
}

// cmdFeeRate prints suggested fee-per-byte for a few confirmation targets.
func cmdFeeRate(nodeURL string) {
	client, err := rpc.NewClient(nodeURL)
	if err != nil {
		fmt.Println("rpc:", err)
		os.Exit(1)
	}
	fmt.Println("Suggested fee-per-byte (atomic units):")
	for _, target := range []int{1, 2, 6} {
		fr, err := client.FeeRate(target)
		if err != nil {
			fmt.Println("feerate:", err)
			os.Exit(1)
		}
		fmt.Printf("  within %d block(s): %d /byte  (floor %d, window %d blocks)\n",
			target, fr.FeePerByte, fr.FloorPerByte, fr.Window)
	}
}

// autoFee derives an absolute fee from the node's suggested fee-per-byte and the
// transaction's measured serialized size. The fee field is fixed-width, so the
// tx size is independent of the fee value: we build once to measure, then scale.
func autoFee(client *rpc.Client, w *wallet.Wallet, dest commit.StealthAddress, amount uint64) uint64 {
	fr, err := client.FeeRate(2)
	if err != nil {
		fmt.Println("feerate:", err)
		os.Exit(1)
	}
	// provisional build (a tiny floor fee) just to measure the serialized size.
	// It is thrown away, so release its input reservations — otherwise the real
	// send below sees its own inputs as reserved and fails "insufficient funds".
	provisional := config.MinFeePerByte * 2048
	t, err := w.CreateTransaction(client, dest, amount, provisional)
	if err != nil {
		fmt.Println("estimate size:", err)
		os.Exit(1)
	}
	w.ReleaseReservation(t)
	size := uint64(len(t.Serialize()))
	fee := fr.FeePerByte * size
	if floor := fr.FloorPerByte * size; fee < floor {
		fee = floor
	}
	return fee
}

// cmdProof produces a receipt proof for each output this wallet received in the
// given transaction, printing a self-contained hex bundle the payer (or anyone)
// can verify offline with `checkproof`.
func cmdProof(path, nodeURL, txid string) {
	if txid == "" {
		fmt.Println("--txid is required")
		os.Exit(1)
	}
	w := loadWallet(path)
	scan(w, nodeURL, statePathFor(path))
	found := 0
	for _, o := range w.Outputs {
		if o.SourceTx != txid {
			continue
		}
		bundle, err := w.ProvePaymentBundle(o)
		if err != nil {
			fmt.Println("proof:", err)
			os.Exit(1)
		}
		found++
		fmt.Printf("Received %s %s in this tx. Receipt proof:\n%s\n",
			config.FormatAmount(o.Amount), config.Ticker, hex.EncodeToString(bundle.Serialize()))
	}
	if found == 0 {
		fmt.Println("no outputs received by this wallet in that transaction")
		os.Exit(1)
	}
}

// cmdProofSent produces a SENDER proof that this wallet paid a recorded outgoing
// transaction (proves "I paid you"). The recipient/third-party verifies it with
// `checkproof` against the recipient address.
func cmdProofSent(path, nodeURL, txid string) {
	if txid == "" {
		fmt.Println("--txid is required")
		os.Exit(1)
	}
	w := loadWallet(path)
	scan(w, nodeURL, statePathFor(path))
	s := w.FindSent(txid)
	if s == nil {
		fmt.Println("no recorded outgoing payment with that txid (run `history`)")
		os.Exit(1)
	}
	bundle, err := w.ProveSpendBundle(s)
	if err != nil {
		fmt.Println("proofsent:", err)
		os.Exit(1)
	}
	fmt.Printf("Proof you paid %s %s to %s…:\n%s\n",
		config.FormatAmount(s.Amount), config.Ticker,
		hex.EncodeToString(s.Dest)[:12], hex.EncodeToString(bundle.Serialize()))
}

// cmdCheckProof verifies a receipt proof against a claimed recipient address,
// offline (no node needed), and prints the proven amount.
func cmdCheckProof(addr, proofHex string) {
	if addr == "" || proofHex == "" {
		fmt.Println("--address and --proof are required")
		os.Exit(1)
	}
	dest, err := parseAddressInput(addr)
	if err != nil {
		fmt.Println("bad address:", err)
		os.Exit(1)
	}
	raw, err := hex.DecodeString(strings.TrimSpace(proofHex))
	if err != nil {
		fmt.Println("bad proof hex")
		os.Exit(1)
	}
	bundle, err := commit.ParseReceiptBundle(raw)
	if err != nil {
		fmt.Println("bad proof:", err)
		os.Exit(1)
	}
	amount, ok := commit.VerifyBundle(dest, bundle)
	if !ok {
		fmt.Println("INVALID: this proof does NOT show a payment to that address")
		os.Exit(1)
	}
	fmt.Printf("VALID: that address received %s %s\n", config.FormatAmount(amount), config.Ticker)
}

// cmdPeers prints the node's currently-connected peers.
func cmdPeers(nodeURL string) {
	client, err := rpc.NewClient(nodeURL)
	if err != nil {
		fmt.Println("rpc:", err)
		os.Exit(1)
	}
	p, err := client.Peers()
	if err != nil {
		fmt.Println("peers:", err)
		os.Exit(1)
	}
	fmt.Printf("%d connected peer(s):\n", p.Count)
	for _, a := range p.Peers {
		fmt.Printf("  %s\n", a)
	}
}

// cmdMempool prints a snapshot of the node's pending-transaction pool.
func cmdMempool(nodeURL string) {
	client, err := rpc.NewClient(nodeURL)
	if err != nil {
		fmt.Println("rpc:", err)
		os.Exit(1)
	}
	st, err := client.Mempool()
	if err != nil {
		fmt.Println("mempool:", err)
		os.Exit(1)
	}
	fmt.Printf("Mempool: %d tx, %d bytes, fees %s %s\n",
		st.Count, st.Bytes, config.FormatAmount(st.TotalFees), config.Ticker)
	if st.Count > 0 {
		fmt.Printf("  fee-rate /byte: min %d, median %d, max %d\n",
			st.MinFeeRate, st.MedFeeRate, st.MaxFeeRate)
	}
}

// cmdOffers lists the live swap order book served by a node.
func cmdOffers(nodeURL string) {
	client, err := rpc.NewClient(nodeURL)
	if err != nil {
		fmt.Println("rpc:", err)
		os.Exit(1)
	}
	offers, err := client.Offers()
	if err != nil {
		fmt.Println("offers:", err)
		os.Exit(1)
	}
	if len(offers) == 0 {
		fmt.Println("no live swap offers")
		return
	}
	fmt.Printf("%d live swap offer(s):\n", len(offers))
	for _, o := range offers {
		id := o.ID()
		fmt.Printf("  %x  give %d %s  ⇄  get %d %s  (maker %x…, expires %s)\n",
			id[:6], o.GiveAmount, o.GiveAsset, o.GetAmount, o.GetAsset,
			o.Maker[:6], time.Unix(o.Expiry, 0).UTC().Format(time.RFC3339))
	}
}

// cmdOffer publishes a signed, PoW-stamped swap offer to the network. The maker
// signing key is derived deterministically from the wallet seed, so anyone who
// later runs the swap can verify the offer came from this wallet.
func cmdOffer(path, nodeURL, giveAsset, getAsset string, giveAmt, getAmt uint64) {
	if giveAmt == 0 || getAmt == 0 || giveAsset == "" || getAsset == "" || giveAsset == getAsset {
		fmt.Println("offer requires --give-asset/--get-asset (distinct) and nonzero --give-amount/--get-amount")
		os.Exit(1)
	}
	client, err := rpc.NewClient(nodeURL)
	if err != nil {
		fmt.Println("rpc:", err)
		os.Exit(1)
	}
	seed := loadSeed(path)
	makerSecret := commit.HashToScalar([]byte("Obscura/offer-key"), seed)
	o := &swapbook.Offer{
		GiveAsset:  giveAsset,
		GetAsset:   getAsset,
		GiveAmount: giveAmt,
		GetAmount:  getAmt,
		Expiry:     time.Now().Add(time.Hour).Unix(),
	}
	o.Sign(makerSecret) // grinds anti-spam PoW + signs
	if err := client.PostOffer(o); err != nil {
		fmt.Println("post offer:", err)
		os.Exit(1)
	}
	id := o.ID()
	fmt.Printf("Posted offer %x: give %d %s ⇄ get %d %s (expires in 1h)\n",
		id[:6], giveAmt, giveAsset, getAmt, getAsset)
}

// scan loads persisted wallet state and scans only NEW blocks since the last
// scan (incremental), then saves state — avoiding a full rescan from genesis.
func scan(w *wallet.Wallet, nodeURL, statePath string) {
	client, err := rpc.NewClient(nodeURL)
	if err != nil {
		fmt.Println("rpc:", err)
		os.Exit(1)
	}
	loadState(w, statePath)
	h := client.Height()
	for i := w.LastScanned() + 1; i <= h; i++ {
		raw, err := client.BlockByHeight(i)
		if err != nil {
			continue
		}
		b, err := block.DeserializeBlock(raw)
		if err != nil {
			continue
		}
		w.ScanBlock(b)
	}
	saveState(w, statePath)
}

func statePathFor(walletPath string) string { return walletPath + ".state" }

// loadState reads the wallet scan state, transparently decrypting it when the
// file is encrypted (a passphrase-protected wallet encrypts its state too — the
// state holds output amounts AND one-time secret keys, so plaintext would leak
// balances and spend authority).
func loadState(w *wallet.Wallet, statePath string) {
	data, err := os.ReadFile(statePath)
	if err != nil {
		return // no state yet
	}
	plain := data
	if keystore.IsEncrypted(data) {
		pass := getPassphrase()
		if len(pass) == 0 {
			fmt.Println("wallet state is encrypted — set OBSCURA_WALLET_PASSPHRASE or pass --passphrase")
			os.Exit(1)
		}
		p, err := keystore.Decrypt(data, pass)
		if err != nil {
			fmt.Println("state:", err)
			os.Exit(1)
		}
		plain = p
	}
	if err := w.RestoreState(plain); err != nil {
		fmt.Println("warning: corrupt wallet state, rescanning from genesis")
	}
}

// saveState writes the wallet scan state, encrypting it whenever a passphrase is
// available (so an encrypted wallet keeps an encrypted state), and writing
// atomically so a crash mid-write cannot corrupt the existing state.
func saveState(w *wallet.Wallet, statePath string) {
	blob := w.MarshalState()
	if pass := getPassphrase(); len(pass) > 0 {
		if enc, err := keystore.Encrypt(blob, pass); err == nil {
			blob = enc
		} else {
			fmt.Println("warning: could not encrypt state:", err)
		}
	}
	if err := atomicWrite(statePath, blob, 0600); err != nil {
		fmt.Println("warning: could not save wallet state:", err)
	}
}

// atomicWrite writes to a temp file and renames over the target, so an
// interrupted write never leaves a half-written (corrupt) file.
func atomicWrite(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// cmdPasswd sets or changes the wallet passphrase: it decrypts with the current
// passphrase (or reads a plaintext wallet) and re-encrypts both the seed and the
// scan state with the new passphrase.
func cmdPasswd(path, newPass string) {
	if newPass == "" {
		newPass = os.Getenv("OBSCURA_WALLET_NEW_PASSPHRASE")
	}
	if newPass == "" {
		fmt.Println("--new-passphrase (or OBSCURA_WALLET_NEW_PASSPHRASE) is required")
		os.Exit(1)
	}
	seed := loadSeed(path) // decrypts with the OLD passphrase, or reads plaintext
	blob, err := keystore.Encrypt(seed, []byte(newPass))
	if err != nil {
		fmt.Println("encrypt:", err)
		os.Exit(1)
	}
	if err := atomicWrite(path, blob, 0600); err != nil {
		fmt.Println("write seed:", err)
		os.Exit(1)
	}
	// re-encrypt the scan state too, if present (decrypt with OLD, encrypt NEW)
	statePath := statePathFor(path)
	if data, err := os.ReadFile(statePath); err == nil {
		plain := data
		if keystore.IsEncrypted(data) {
			p, derr := keystore.Decrypt(data, getPassphrase())
			if derr != nil {
				fmt.Println("state decrypt:", derr)
				os.Exit(1)
			}
			plain = p
		}
		enc, eerr := keystore.Encrypt(plain, []byte(newPass))
		if eerr != nil {
			fmt.Println("state encrypt:", eerr)
			os.Exit(1)
		}
		if err := atomicWrite(statePath, enc, 0600); err != nil {
			fmt.Println("write state:", err)
			os.Exit(1)
		}
	}
	fmt.Println("Passphrase updated. The seed and scan state are now encrypted with the new passphrase.")
}

func parseOBX(s string) uint64 {
	parts := strings.SplitN(s, ".", 2)
	whole, _ := strconv.ParseUint(parts[0], 10, 64)
	atomic := whole * config.AtomicPerCoin
	if len(parts) == 2 {
		frac := parts[1]
		for len(frac) < 12 {
			frac += "0"
		}
		frac = frac[:12]
		f, _ := strconv.ParseUint(frac, 10, 64)
		atomic += f
	}
	return atomic
}

func defaultWalletPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".obscura", "wallet.seed")
}
