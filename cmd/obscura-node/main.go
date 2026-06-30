// Command obscura-node runs a full Obscura node: chain state, mempool, P2P
// gossip, JSON-RPC, and an optional built-in CPU miner.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"math"
	mrand "math/rand"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"filippo.io/edwards25519"
	"golang.org/x/net/netutil"

	"obscura/pkg/chain"
	"obscura/pkg/commit"
	"obscura/pkg/config"
	"obscura/pkg/mempool"
	"obscura/pkg/miner"
	"obscura/pkg/nanorpc"
	"obscura/pkg/p2p"
	"obscura/pkg/pow"
	"obscura/pkg/rpc"
	"obscura/pkg/swapbook"
	"obscura/pkg/swapd"
	"obscura/pkg/tx"
	"obscura/pkg/wallet"
)

func main() {
	var (
		datadir   = flag.String("datadir", defaultDataDir(), "data directory")
		p2pAddr   = flag.String("p2p", fmt.Sprintf("0.0.0.0:%d", config.DefaultP2PPort), "P2P listen address")
		rpcAddr   = flag.String("rpc", fmt.Sprintf("127.0.0.1:%d", config.DefaultRPCPort), "RPC listen address")
		seeds     = flag.String("seeds", "", "comma-separated seed peers host:port")
		mine      = flag.Bool("mine", false, "enable the built-in CPU miner")
		mineAddr  = flag.String("mine-address", "", "address to mine to (hex); default uses node miner wallet")
		referrer  = flag.String("referrer", "", "referrer address tag (hex) for the sharing bonus")
		torProxy  = flag.String("tor-proxy", "", "route P2P over Tor via this SOCKS5 proxy (e.g. 127.0.0.1:9050)")
		onionAdr  = flag.String("onion-address", "", "this node's .onion address to advertise (with --tor-proxy)")
		advertise = flag.String("advertise", "", "public address to announce to peers (host:port); auto-discovered from peers if empty")
		maturity  = flag.Uint64("coinbase-maturity", config.CoinbaseMaturity, "blocks a coinbase must age before spending (NETWORK PARAM — all nodes must match; lower for devnets)")

		// Auto-liquid mining rewards: when mining, automatically post OBX→XNO swap
		// OFFERS from spendable rewards into the P2P order book (off-chain gossip, NOT
		// consensus). OFF by default for miner privacy (audit #16); opt in with
		// OBX_AUTO_LIQUIDITY=1. This flag force-disables it regardless of env/config.
		noAutoLiquidity = flag.Bool("no-auto-liquidity", false, "force-disable auto-posting OBX→XNO swap offers from mining rewards (ON by default; this flag, or OBX_AUTO_LIQUIDITY=0, opts out for miner privacy)")

		// Cross-chain swap backends. Obscura HARDCODES NO third-party RPC into the swap
		// logic; the operator selects one. As a convenience, --nano-rpc accepts a built-in
		// preset NAME from a working public-RPC pick-list OR a full custom URL. DEFAULT is
		// "public" (rainstorm + a public read fallback chain); set off to disable XNO exec.
		nanoRPC = flag.String("nano-rpc", func() string {
			if v := os.Getenv("OBX_NANO_RPC"); v != "" {
				return v
			}
			return "public" // DEFAULT: rainstorm + the public fallback chain (operator can override or set 'off')
		}(), "Nano RPC: preset (rainstorm|somenano|nanoto|natrium|public) or a full URL; DEFAULT 'public'; 'off' disables XNO execution")
		nanoList    = flag.Bool("nano-rpc-list", false, "print the built-in public Nano RPC presets and exit")
		showVersion = flag.Bool("version", false, "print this node's release version and exit (used by the installer to detect upgrades)")
		nanoAuth    = flag.String("nano-rpc-auth", os.Getenv("OBX_NANO_RPC_AUTH"), "optional Authorization header value for the Nano RPC")
		nanoWallet  = flag.String("nano-wallet", os.Getenv("OBX_NANO_WALLET"), "Nano node wallet id used as the funding source when locking XNO")
		nanoAccount = flag.String("nano-account", os.Getenv("OBX_NANO_ACCOUNT"), "Nano funding account (nano_...) inside --nano-wallet")
		nanoWorkURL = flag.String("nano-work-url", os.Getenv("OBX_NANO_WORK_URL"), "override the work_generate endpoint (defaults to the preset's work URL or --nano-rpc)")
		// LOCAL-KEY funding secret for an in-node TAKER paying REAL XNO through a PUBLIC
		// Nano RPC (which gives no node-managed wallet). This is the raw 32-byte ed25519
		// scalar (hex) of the TAKER's funding XNO account: when set (and --nano-wallet is
		// empty) the node SIGNS the funding send LOCALLY. Only the BUYER (taker) needs it;
		// a MAKER (seller) sweeps with its seed-derived key and needs NO fund secret. It is
		// a SECRET — validated at startup, never logged.
		nanoFundSecret = flag.String("nano-fund-secret", os.Getenv("OBX_NANO_FUND_SECRET"), "TAKER local funding XNO account secret (raw 32-byte ed25519 scalar hex); signs the XNO lock send locally when using a public --nano-rpc with no --nano-wallet (makers do not need this)")
		// Where the maker sweeps the XNO it receives from a settled OBX→XNO swap. Empty
		// derives a recoverable nano_ address from the miner seed (Obscura/xno-proceeds/v1);
		// set it to an external/cold nano_ address to receive proceeds off-node. It is
		// VALIDATED at startup (a bad address refuses to enable real-Nano OBX→XNO offers).
		xnoSweepDest = flag.String("xno-sweep-dest", os.Getenv("OBX_XNO_SWEEP_DEST"), "nano_ address the maker sweeps swept XNO to (empty = derive from miner seed)")

		// Desktop-app mode: serve the embedded website (wallet + swap + explorer UI)
		// on a local port and open the browser, so a downloaded binary is a turnkey
		// app with zero configuration. See ui.go.
		ui     = flag.Bool("ui", false, "serve the embedded wallet/swap/mining web UI locally and open the browser (turnkey desktop-app mode)")
		uiAddr = flag.String("ui-addr", "127.0.0.1:8088", "local address to serve the embedded web UI on (with --ui)")
	)
	flag.Parse()
	if *showVersion {
		fmt.Println(p2p.SoftwareVersion)
		return
	}
	if *nanoList {
		fmt.Print(swapd.NanoPresetList())
		return
	}
	config.CoinbaseMaturity = *maturity

	// NOTE: --ui (turnkey desktop mode) deliberately does NOT relax the prototype-PoW
	// start guard. The default build is the canonical RandomX backend, so a properly
	// packaged desktop binary boots cleanly; only a binary mis-built with -tags protopow
	// would hit the guard, and silently downgrading that to the insecure backend (audit
	// #27) is exactly the footgun we refuse. Devnets opt in explicitly with
	// OBX_ALLOW_PROTOTYPE_POW=1.

	if err := os.MkdirAll(*datadir, 0700); err != nil {
		log.Fatalf("datadir: %v", err)
	}

	c, err := chain.New(*datadir)
	if err != nil {
		log.Fatalf("chain: %v", err)
	}
	defer c.Close()
	log.Printf("%s (%s) node | backend=%s | pow=%s | height=%d | emitted=%s %s",
		config.CoinName, config.Ticker, config.AccumulatorBackend, pow.BackendName, c.Height(),
		config.FormatAmount(c.Emitted()), config.Ticker)
	guardPrototypePoW()

	mp := mempool.New(c)
	node := p2p.NewNode(*p2pAddr, c, mp, filepath.Join(*datadir, "peers.json"))
	if *advertise != "" {
		node.SetAdvertise(*advertise) // pin public address (e.g. a server's public IP)
		log.Printf("advertising public address %s to peers", *advertise)
	}
	if *torProxy != "" {
		d, err := p2p.NewTorDialer(*torProxy)
		if err != nil {
			log.Fatalf("tor: %v", err)
		}
		node.SetTransport(d, *onionAdr, true)
		log.Printf("Tor transport ENABLED via %s (advertising %q, onion-only)", *torProxy, *onionAdr)
	}
	var seedList []string
	if *seeds != "" {
		seedList = strings.Split(*seeds, ",")
	} else if len(config.DefaultSeeds) > 0 {
		// Zero-config bootstrap: with no --seeds, fall back to the embedded default
		// seed list so a freshly-downloaded node can find the network.
		seedList = append(seedList, config.DefaultSeeds...)
		log.Printf("no --seeds given; using %d embedded default seed(s)", len(seedList))
	}
	if err := node.Start(seedList); err != nil {
		log.Fatalf("p2p: %v", err)
	}
	log.Printf("P2P listening on %s (seeds: %v)", *p2pAddr, seedList)

	srv := rpc.NewServer(c, mp, node.BroadcastTx)
	srv.SetOfferBook(node)                                          // expose the swap order book over RPC
	srv.SetPriceHistPath(filepath.Join(*datadir, "pricehist.json")) // persist the chart history across restarts
	srv.SetMetricsPath(filepath.Join(*datadir, "metrics.json"))     // persist explorer sparkline series + swap volume

	// XNO↔OBX swap leg: only enabled when the operator selects a Nano RPC (preset name or
	// URL). Nothing is hardcoded into the swap logic — a preset is an explicit operator
	// choice. Without a selection the order book still works and only XNO execution is off.
	var realNano swapd.NanoClient  // nil unless a real --nano-rpc client is selected
	var realNanoRPC *swapd.NanoRPC // concrete client for the XNO proceeds wallet (balance/receivable/send)
	var nanoPub *nanorpc.Client    // secret-free Nano client for the NON-CUSTODIAL browser swap relay
	// Explicit opt-out of the default public Nano RPC (privacy / air-gapped operators).
	if s := strings.ToLower(strings.TrimSpace(*nanoRPC)); s == "off" || s == "none" || s == "disabled" || s == "false" {
		*nanoRPC = ""
	}
	if *nanoRPC != "" {
		cfg, isPreset := swapd.ResolveNanoSelector(*nanoRPC)
		cfg.AuthHeader = *nanoAuth
		cfg.WalletID = *nanoWallet
		cfg.Source = *nanoAccount
		cfg.FundSecretHex = *nanoFundSecret // TAKER local-key funding secret (validated below; never logged)
		if *nanoWorkURL != "" {
			cfg.WorkURL = *nanoWorkURL
		}
		// Secret-free client (no wallet/source/fund-secret) for the browser relay's
		// read + publish endpoints: the browser signs its own Nano blocks; this only
		// reads state, generates work, and processes already-signed blocks. It FAILS
		// OVER across the SAME curated endpoint list the node software uses
		// (swapd.PublicNanoRPCs): the operator's chosen endpoint first, then
		// rainstorm → somenano → nanoto (work routed to rainstorm), deduped. So if the
		// primary flakes, the browser swap keeps working on the node's own fallbacks.
		nanoChain := []nanorpc.Config{{URL: cfg.URL, AuthHeader: cfg.AuthHeader, WorkURL: cfg.WorkURL}}
		for _, p := range swapd.PublicNanoRPCs {
			if p.URL == cfg.URL {
				continue // primary already first
			}
			work := p.WorkURL
			if work == "" {
				work = p.URL
			}
			nanoChain = append(nanoChain, nanorpc.Config{URL: p.URL, WorkURL: work})
		}
		if np, nerr := nanorpc.NewMulti(nanoChain); nerr == nil {
			nanoPub = np
			log.Printf("XNO browser-relay Nano client: %d endpoint(s) with failover (primary %s)", len(nanoChain), cfg.URL)
		} else {
			log.Printf("WARNING: secret-free Nano relay client not built (%v); browser /swaps/nano/* disabled", nerr)
		}
		// NewNanoRPC validates --nano-fund-secret (canonical 32-byte ed25519 scalar) and
		// drops the raw hex from its config, so the secret never survives past this call
		// as a string. A bad secret fails fast HERE, before the node serves anything.
		nano, err := swapd.NewNanoRPC(cfg)
		if err != nil {
			log.Fatalf("nano swap backend: %v", err)
		}
		if *nanoFundSecret != "" {
			// Confirm to the operator the local-key TAKER lock path is armed — WITHOUT
			// echoing the secret: log only the derived public funding address.
			if addr, derr := swapd.NanoRPCFundAddress(nano); derr == nil {
				log.Printf("XNO local-key TAKER funding ENABLED (funding account %s); the buyer pays REAL XNO from this account", addr)
			} else {
				log.Printf("XNO local-key TAKER funding ENABLED (funding account derivation: %v)", derr)
			}
		}
		realNano = nano
		realNanoRPC = nano
		src := "custom URL"
		if isPreset {
			src = "preset " + *nanoRPC
		}
		if ver, err := nano.Version(); err != nil {
			log.Printf("WARNING: Nano RPC %s (%s) not reachable (%v) — XNO swap execution will fail until it is", cfg.URL, src, err)
		} else {
			log.Printf("XNO swap leg ENABLED via %s %s (node: %s)", src, cfg.URL, ver)
		}
		srv.SetNanoBackend(nano)
	} else {
		log.Printf("XNO swap execution DISABLED (no --nano-rpc; try --nano-rpc-list); swap order book still active")
	}
	srv.SetBlockBroadcaster(node.BroadcastBlock) // enable external-miner endpoints
	srv.SetPeerProvider(node)                    // expose connected-peer info over RPC

	// Wire the trustless XNO<->OBX atomic-swap engine so /liquidity, /swaps/active,
	// and /swaps/take reflect liquidity, every swap step, and taker-initiated trades.
	// The OBX leg funds from the node's miner wallet seed; the Nano leg uses the real
	// --nano-rpc client when configured. On testnet/devnet a MockNano fallback completes
	// every step without real XNO (demos). On MAINNET, execution is refused below if no
	// real Nano client is configured (a mock XNO leg against real OBX would lose value).
	swapSeed := loadMinerSeed(*datadir)
	// XNO PROCEEDS WALLET: give the RPC the miner seed (to derive the recoverable
	// XNO proceeds account + sign withdrawals in-process) and the real Nano client
	// for balance/receivable/send. With no --nano-rpc the ledger is nil and
	// /xno/account serves the derived address on the mock backend. We pass an
	// explicit nil (not a typed-nil *NanoRPC) so the mock-backend check stays honest.
	if realNanoRPC != nil {
		srv.SetXNO(swapSeed, realNanoRPC)
	} else {
		srv.SetXNO(swapSeed, nil)
	}
	// MAINNET SAFETY (LIVE-ONLY policy): never back swap EXECUTION with the MockNano
	// stand-in. On a live chain the OBX leg moves REAL value, so a mocked XNO leg would
	// let a maker hand over real OBX and receive nothing. realNano is nil unless --nano-rpc
	// was set above; when nil, nanoForSwaps inside the wiring REFUSES to start the swap
	// engine (returning errSwapMockRefused) UNLESS OBX_ALLOW_MOCK_NANO=1 opts into MockNano
	// for the value-less local test chain — in which case the wiring logs a loud WARNING.
	// On refusal the order book still gossips offers; operators enable real swaps by
	// pointing --nano-rpc at a Nano node.
	swapCoord, swapRelay, swapFee, err := wireSwapCoordinator(c, node, swapSeed, realNano, *xnoSweepDest, *datadir)
	if err != nil {
		log.Printf("WARNING: swap engine NOT wired (%v); /swaps/* will be empty", err)
	} else {
		srv.SetSwapCoordinator(swapCoord, swapFee)
		// NON-CUSTODIAL browser swap: expose the relay + secret-free Nano client so a
		// browser taker can run the full swap (it signs every leg; node only relays).
		srv.SetSwapRelay(swapRelay, nanoPub)
		defer swapCoord.Stop()
	}

	// Wrap the RPC handler with a tiny /auto-liquidity observability endpoint. Cheap,
	// lock-free read of the auto-liquidity loop's status counters. Falls through to
	// the RPC server for every other path.
	rpcHandler := srv.Handler()
	wrapped := http.NewServeMux()
	wrapped.HandleFunc("/auto-liquidity", handleAutoLiquidityStatus)
	wrapped.Handle("/", rpcHandler)

	// Audit #44: bound RPC abuse. Per-IP token-bucket rate limiting (keyed on the
	// non-forgeable peer IP; loopback/operator exempt) + a global concurrent-
	// connection cap via netutil.LimitListener. Both env-tunable.
	rl := newRateLimiter()
	maxConns := envIntPositive("OBX_RPC_MAX_CONNS", 1024)
	httpSrv := &http.Server{
		Addr:              *rpcAddr,
		Handler:           rl.middleware(wrapped),
		ReadTimeout:       15 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}
	rpcLn, err := net.Listen("tcp", *rpcAddr)
	if err != nil {
		log.Fatalf("rpc listen: %v", err)
	}
	rpcLn = netutil.LimitListener(rpcLn, maxConns)
	go func() {
		log.Printf("RPC listening on http://%s (rate %g req/s, burst %g, max %d conns)", *rpcAddr, rl.rate, rl.burst, maxConns)
		if err := httpSrv.Serve(rpcLn); err != nil {
			log.Fatalf("rpc: %v", err)
		}
	}()

	// Desktop-app UI: AFTER the RPC server is listening, serve the embedded website
	// (wallet + swap + explorer) on a local port with a local /api/explorer proxy
	// that forwards to this node's in-process RPC, then open the browser. Best-effort
	// and isolated to ui.go; without --ui the node behaves exactly as before.
	if *ui {
		startUI(*uiAddr, *rpcAddr)
	}

	if *mine {
		dest, minerSeed := resolveMineAddress(*datadir, *mineAddr)
		var refTag []byte
		if *referrer != "" {
			refTag, _ = hex.DecodeString(*referrer)
		}
		log.Printf("Mining enabled")
		go mineLoop(c, mp, node, dest, refTag)

		// Auto-liquid mining rewards: bootstrap swap liquidity from rewards. Only
		// when we have the miner wallet SEED (i.e. mining to the node's own wallet,
		// not an external --mine-address we cannot sign for), auto-liquidity is
		// enabled in config, and it was not force-disabled with --no-auto-liquidity.
		switch {
		case *noAutoLiquidity || !config.AutoSwapLiquidity:
			log.Printf("auto-liquidity DISABLED (offers will not be auto-posted)")
		case minerSeed == nil:
			log.Printf("auto-liquidity DISABLED (mining to an external address whose seed this node lacks)")
		default:
			log.Printf("auto-liquidity ENABLED: auto-posting OBX→XNO offers from mining rewards (seed rate %.4g XNO/OBX, every %ds)",
				config.AutoLiquiditySeedRateXNO, config.AutoLiquidityIntervalSec)
			log.Printf("auto-liquidity PRIVACY WARNING: selling mined OBX for XNO publishes a miner→XNO " +
				"link on the public Nano ledger, which can deanonymize your mining (opt out: --no-auto-liquidity or OBX_AUTO_LIQUIDITY=0).")
			go autoLiquidityLoop(c, node, minerSeed)
		}
	}

	// Graceful shutdown: on SIGINT/SIGTERM snapshot the EXACT tip before exiting so a
	// planned restart (systemctl restart, redeploy) restores state directly and
	// replays ~0 blocks instead of re-running class-group validation over the whole
	// chain since the last height-triggered snapshot. defer c.Close() then runs on
	// return. (A crash skips this; SnapshotInterval bounds that replay instead.)
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)
	s := <-sigc
	log.Printf("received %s — saving shutdown snapshot at height %d", s, c.Height())
	if err := c.SaveSnapshot(); err != nil {
		log.Printf("shutdown snapshot failed: %v", err)
	} else {
		log.Printf("shutdown snapshot saved")
	}
}

func mineLoop(c *chain.Chain, mp *mempool.Mempool, node *p2p.Node, dest commit.StealthAddress, refTag []byte) {
	target := time.Duration(config.TargetBlockTime) * time.Second
	var prevStart time.Time

	// Mining progress reporter: RandomX solves can be minutes apart, so without this the
	// console is silent between blocks ("connected, then nothing"). Every 20s it shows
	// the live hashrate, the current height/difficulty, peer count, and how much THIS
	// node has mined + earned so far.
	var minedN atomic.Int64
	var minedReward atomic.Uint64 // total OBX (atomic units) this node has minted
	var lastBlockNS atomic.Int64  // unix-nano of the last solve (0 = none yet)
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		prevH, prevT := miner.HashCount.Load(), time.Now()
		for range t.C {
			now := time.Now()
			h := miner.HashCount.Load()
			rate := float64(h-prevH) / now.Sub(prevT).Seconds()
			prevH, prevT = h, now
			last := "no block yet"
			if lb := lastBlockNS.Load(); lb > 0 {
				last = time.Since(time.Unix(0, lb)).Truncate(time.Second).String() + " ago"
			}
			log.Printf("⏳ mining @ height %d | diff=%d | %s | peers=%d | you mined %d block(s) = %s %s | last solve: %s",
				c.Height(), c.ExpectedDifficulty(), fmtHashrate(rate), node.PeerCount(),
				minedN.Load(), config.FormatAmount(uint64(minedReward.Load())), config.Ticker, last)
		}
	}()

	for {
		// PACE block production to the target interval. In production this is a no-op:
		// PoW retargeting already makes a solve take ~TargetBlockTime, so the elapsed
		// time exceeds `target` and we never sleep. But under an artificially LOW or
		// FIXED difficulty (devnet / load test), an unpaced miner grinds hundreds of
		// blocks per minute — monopolizing a core and flooding peers with blocks, which
		// STARVES P2P (peers drop and hit the minutes-long reconnect backoff) and the
		// RPC (slow tx admission). Pacing produces one full block per interval, freeing
		// CPU for gossip + ingestion — the root-cause fix for the cross-node peer drops.
		if pace := target - time.Since(prevStart); !prevStart.IsZero() && pace > 0 {
			time.Sleep(pace)
		}
		prevStart = time.Now()

		txs := mp.Select(1000)
		// Empty mempool: a small extra floor keeps coinbase maturing without grinding
		// empty blocks (pacing above already bounds the rate).
		if len(txs) == 0 {
			time.Sleep(time.Second)
		}
		fees := chain.CollectedFees(txs)
		minted := c.ExpectedCoinbaseMinted(fees, refTag)
		cb, err := wallet.BuildCoinbaseTo(dest, c.Height()+1, minted, refTag)
		if err != nil {
			log.Printf("coinbase: %v", err)
			time.Sleep(time.Second)
			continue
		}
		all := append([]*tx.Transaction{cb}, txs...)
		tmpl, err := c.BlockTemplate(all)
		if err != nil {
			log.Printf("template: %v", err)
			time.Sleep(time.Second)
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		// re-template if a new block arrives mid-mining
		stop := make(chan struct{})
		go func() {
			startH := tmpl.Header.Height
			t := time.NewTicker(500 * time.Millisecond)
			defer t.Stop()
			for {
				select {
				case <-stop:
					return
				case <-t.C:
					if c.Height() >= startH {
						cancel()
						return
					}
				}
			}
		}()
		found := miner.MineSeed(ctx, tmpl, c.PoWSeed(tmpl.Header.Height), 0)
		close(stop)
		cancel()
		if !found {
			continue
		}
		if err := c.AddBlock(tmpl); err != nil {
			continue
		}
		mp.Remove(tmpl.Txs)
		// Broadcast asynchronously: a slow/back-pressured peer must never stall
		// block production (per-peer write locks are shared with tx relay, which
		// can pile up under high load). Peers also receive blocks via normal sync.
		go node.BroadcastBlock(tmpl)
		minedN.Add(1)
		minedReward.Add(minted)
		lastBlockNS.Store(time.Now().UnixNano())
		log.Printf("⛏  MINED block %d | diff=%d | txs=%d | reward=%s %s | supply=%s %s | peers=%d | YOU: %d block(s) = %s %s",
			tmpl.Header.Height, tmpl.Header.Difficulty, len(tmpl.Txs),
			config.FormatAmount(minted), config.Ticker,
			config.FormatAmount(c.Emitted()), config.Ticker, node.PeerCount(),
			minedN.Load(), config.FormatAmount(uint64(minedReward.Load())), config.Ticker)
	}
}

// fmtHashrate renders a PoW hashrate (hashes/sec) compactly for the miner progress line.
func fmtHashrate(hps float64) string {
	switch {
	case hps >= 1e6:
		return fmt.Sprintf("~%.2f MH/s", hps/1e6)
	case hps >= 1e3:
		return fmt.Sprintf("~%.2f kH/s", hps/1e3)
	default:
		return fmt.Sprintf("~%.0f H/s", hps)
	}
}

// autoLiquidityStatus is a cheap, lock-free observability snapshot of the
// auto-liquidity loop, exposed via the RPC /auto-liquidity endpoint and logged.
type autoLiquidityStatus struct {
	posted   atomic.Int64  // total auto-offers ever admitted by this node
	live     atomic.Int64  // currently-outstanding auto-offers (capped)
	rateBits atomic.Uint64 // last OBX→XNO rate used (float64 bits)
}

// liquidityState is the single shared status for this process (one miner wallet).
var liquidityState autoLiquidityStatus

// handleAutoLiquidityStatus serves a tiny JSON snapshot of the auto-liquidity loop
// for observability: how many auto-offers were posted, how many are live, the last
// OBX→XNO rate used, and whether the feature is enabled at all.
func handleAutoLiquidityStatus(w http.ResponseWriter, r *http.Request) {
	enabled := config.AutoSwapLiquidity
	rate := math.Float64frombits(liquidityState.rateBits.Load())
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"enabled":%t,"posted":%d,"live":%d,"rate_xno_per_obx":%g}`+"\n",
		enabled, liquidityState.posted.Load(), liquidityState.live.Load(), rate)
}

// autoLiquidityLoop periodically reads the miner wallet's spendable OBX and, if
// there is un-offered balance, constructs + admits + gossips OBX→XNO swap offers
// into the P2P order book — so mining bootstraps liquidity and price discovery.
//
// NON-CONSENSUS: offers are off-chain gossip (node.PostOffer adds to the local book
// AND broadcasts msgSwapOffer to peers), never a chain transaction. The loop NO-OPs
// cleanly when there is no spendable balance, no book, or the cap is reached. It
// never SPENDS the rewards on-chain — posting an offer is only an intent; actually
// executing a taken swap is the separate two-party atomic-swap flow (pkg/swapd).
func autoLiquidityLoop(c *chain.Chain, node *p2p.Node, seed []byte) {
	// Maker key MUST match the web wallet's derivation (cmd/obscura-wasm: obxBuildOffer)
	// so auto-offers are attributable to the same maker identity as a manually-posted
	// offer from this seed.
	makerSecret := commit.HashToScalar([]byte("Obscura/maker/v1"), seed)
	makerPub := new(edwards25519.Point).ScalarBaseMult(makerSecret).Bytes()

	// A view-only scan wallet derived from the seed: we only need balances, never
	// to spend. We rescan incrementally from lastScanned each tick.
	w := wallet.FromSeed(seed)
	var scanned uint64

	tick := time.Duration(config.AutoLiquidityIntervalSec) * time.Second
	if tick <= 0 {
		tick = time.Minute
	}
	// Auto-offers churn so the price can MOVE: a short TTL (3 ticks) means old
	// rungs expire and are re-posted at the walked mid, instead of one frozen
	// price sitting in the book forever.
	offerTTL := 3 * tick
	if offerTTL < 90*time.Second {
		offerTTL = 90 * time.Second
	}
	// mid is the maker's evolving mid price (XNO per OBX). It RANDOM-WALKS each
	// tick (clamped to a band around the seed) so the market is dynamic and the
	// price chart actually moves. rng is seeded off the maker key + wall clock.
	mid := config.AutoLiquiditySeedRateXNO
	rng := mrand.New(mrand.NewSource(time.Now().UnixNano() ^ int64(makerPub[0]) ^ (int64(makerPub[1]) << 8)))
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		<-t.C

		// Catch the scan wallet up to the tip so SpendableOutputs reflects matured
		// coinbase. Bounded per-tick work: we only scan blocks we haven't seen.
		tip := c.Height()
		for h := scanned + 1; h <= tip; h++ {
			b, ok := c.BlockByHeight(h)
			if !ok {
				break // body pruned / not available — stop here, retry next tick
			}
			w.ScanBlock(b)
			scanned = h
		}

		// Spendable (mature, unlocked) OBX at the next height.
		spendHeight := c.Height() + 1
		var spendable uint64
		for _, o := range w.SpendableOutputs(spendHeight) {
			spendable += o.Amount
		}
		if spendable == 0 {
			continue // no-op: nothing to offer yet
		}

		// EVOLVE THE MID PRICE: a bounded random walk so the market is dynamic (the
		// price chart moves) instead of sitting on one fixed number. Anchor to the
		// book's current best when one exists (so we track the real market) but
		// always nudge it, then clamp to a sane band around the seed rate.
		if anchored := bookOrSeedRate(node); anchored > 0 {
			mid = 0.5*mid + 0.5*anchored // ease toward the live book, then walk
		}
		mid *= 1 + (rng.Float64()*2-1)*config.AutoLiquidityPriceStepPct
		lo, hi := config.AutoLiquiditySeedRateXNO*0.4, config.AutoLiquiditySeedRateXNO*2.5
		if mid < lo {
			mid = lo
		}
		if mid > hi {
			mid = hi
		}
		liquidityState.rateBits.Store(math.Float64bits(mid))

		// How much OBX is already tied up in our outstanding auto-offers, and how
		// many we have. We track our own offers by maker pubkey.
		mine := node.MakerOffers(makerPub)
		var offeredOBX uint64
		for _, o := range mine {
			offeredOBX += offerOBXAtomic(o.GiveAmount) // convert offer give-units back to atomic
		}
		liquidityState.live.Store(int64(len(mine)))
		want := config.AutoLiquidityMaxOffers - len(mine)
		if want <= 0 {
			continue // full depth — refresh next tick as short-TTL rungs expire
		}

		// Budget: at most AutoLiquidityMaxFraction of spendable may be outstanding.
		budget := uint64(float64(spendable) * config.AutoLiquidityMaxFraction)
		if offeredOBX >= budget {
			continue // already at our target depth
		}
		room := budget - offeredOBX

		baseChunk := uint64(config.AutoLiquidityChunkOBX * float64(config.AtomicPerCoin))
		if baseChunk == 0 {
			baseChunk = config.AtomicPerCoin
		}
		minChunk := config.AtomicPerCoin / 100 // 0.01 OBX floor

		// POST A COMPETITIVE LADDER around the mid. Rung 0 is the most AGGRESSIVE
		// price (fewest XNO per OBX → best for a taker → matched FIRST); each
		// further rung steps the price up by LadderStep, giving the book real
		// depth at distinct, competing levels rather than one flat price. Chunk
		// sizes are varied ±30% so the depth isn't uniform/static either.
		posted, lowRate, highRate := 0, 0.0, 0.0
		for i := 0; i < want && room >= minChunk; i++ {
			level := mid * (1 + float64(i)*config.AutoLiquidityLadderStepPct)
			if level <= 0 {
				continue
			}
			chunk := baseChunk * uint64(70+rng.Intn(61)) / 100 // 0.7x–1.3x
			if chunk < minChunk {
				chunk = minChunk
			}
			if chunk > room {
				chunk = room
			}
			giveUnits, getUnits, ok := offerAmountsFor(chunk, level)
			if !ok {
				continue
			}
			o := swapbook.BuildSignedOffer("OBX", "XNO", giveUnits, getUnits, offerTTL, makerSecret)
			if err := node.PostOffer(o); err != nil {
				log.Printf("auto-liquidity: offer rejected: %v", err)
				continue
			}
			room -= chunk
			posted++
			liquidityState.posted.Add(1)
			liquidityState.live.Add(1)
			if lowRate == 0 || level < lowRate {
				lowRate = level
			}
			if level > highRate {
				highRate = level
			}
		}
		if posted > 0 {
			log.Printf("auto-liquidity: posted %d competitive OBX→XNO rungs, mid %.6g XNO/OBX, ladder [%.6g..%.6g], depth %d/%d, total posted=%d",
				posted, mid, lowRate, highRate, len(mine)+posted, config.AutoLiquidityMaxOffers, liquidityState.posted.Load())
		}
	}
}

// offerDecOBX / offerDecXNO are the human-decimal scales used on the wire for offer
// amounts (so the website/explorer read our auto-offers at the same human rate).
func offerDecOBX() int { return config.AutoLiquidityDecimals["OBX"] }
func offerDecXNO() int { return config.AutoLiquidityDecimals["XNO"] }

// offerOBXAtomic converts an offer GiveAmount (in OBX human-decimal units, 10^DEC.OBX)
// back to OBX on-chain atomic units (10^12), for budgeting against spendable balance.
func offerOBXAtomic(giveUnits uint64) uint64 {
	// atomic = giveUnits * 10^(12 - DEC.OBX). For DEC.OBX=8 that is *10^4.
	exp := 12 - offerDecOBX()
	if exp >= 0 {
		return giveUnits * pow10u(exp)
	}
	return giveUnits / pow10u(-exp)
}

// offerAmountsFor converts a chunk of OBX (atomic, 10^12) and an OBX→XNO rate
// (XNO per OBX, human) into the offer's (giveAmount[OBX-units], getAmount[XNO-units])
// in the ecosystem's human-decimal scales. XNO get-amount is capped at uint64 (the
// Offer/NanoClient amount type); for a placeholder seed rate of ~1 this is ample.
func offerAmountsFor(chunkAtomic uint64, rate float64) (give, get uint64, ok bool) {
	humanOBX := float64(chunkAtomic) / float64(config.AtomicPerCoin) // whole OBX
	if humanOBX <= 0 {
		return 0, 0, false
	}
	give = uint64(humanOBX * math.Pow10(offerDecOBX()))
	humanXNO := humanOBX * rate
	getF := humanXNO * math.Pow10(offerDecXNO())
	if give == 0 || getF < 1 || getF >= float64(^uint64(0)) {
		return 0, 0, false // not expressible in uint64 / too small
	}
	return give, uint64(getF), true
}

// bookOrSeedRate returns the current best OBX→XNO rate (XNO per OBX, human units)
// from the order book, or the configured seed rate when no such offer exists.
func bookOrSeedRate(node *p2p.Node) float64 {
	best := 0.0
	for _, o := range node.Offers() {
		if o.GiveAsset != "OBX" || o.GetAsset != "XNO" || o.GiveAmount == 0 {
			continue
		}
		hg := float64(o.GiveAmount) / math.Pow10(offerDecOBX())
		hr := float64(o.GetAmount) / math.Pow10(offerDecXNO())
		if hg <= 0 {
			continue
		}
		r := hr / hg
		if r > best { // best for the taker == most XNO per OBX (mirrors Book.Best)
			best = r
		}
	}
	if best > 0 {
		return best
	}
	return config.AutoLiquiditySeedRateXNO
}

// pow10u returns 10^n as a uint64 for small non-negative n.
func pow10u(n int) uint64 {
	out := uint64(1)
	for i := 0; i < n; i++ {
		out *= 10
	}
	return out
}

// resolveMineAddress returns the address to mine to and, when mining to the node's
// own persistent wallet, that wallet's SEED (so the caller can derive the maker key
// and scan for spendable rewards for auto-liquidity). For an explicit external
// --mine-address the seed is nil: we can credit it but cannot sign on its behalf.
func resolveMineAddress(datadir, mineAddr string) (commit.StealthAddress, []byte) {
	if mineAddr != "" {
		if a, err := commit.ParseHumanAddress(mineAddr); err == nil {
			return a, nil
		}
		b, err := hex.DecodeString(mineAddr)
		if err != nil {
			log.Fatalf("bad mine-address: not a valid Base58 address or hex")
		}
		a, err := commit.DecodeAddress(b)
		if err != nil {
			log.Fatalf("bad mine-address: %v", err)
		}
		return a, nil
	}
	seed := loadMinerSeed(datadir)
	w := wallet.FromSeed(seed)
	log.Printf("mining to %s", hex.EncodeToString(w.AddressBytes()))
	return w.Address(), seed
}

// loadMinerSeed reads (or first-time creates) the node's persistent miner wallet
// seed at <datadir>/miner.seed. It is used both for mining-to-own-wallet and to
// fund/receive the OBX leg of swaps (so swaps work even without --mine).
func loadMinerSeed(datadir string) []byte {
	seedPath := filepath.Join(datadir, "miner.seed")
	seed, err := os.ReadFile(seedPath)
	if err == nil {
		return seed
	}
	// generate a high-entropy seed with crypto/rand and create exclusively.
	seed = make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		log.Fatalf("miner seed rng: %v", err)
	}
	f, err := os.OpenFile(seedPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		log.Fatalf("create miner seed: %v", err)
	}
	if _, err := f.Write(seed); err != nil {
		log.Fatalf("write miner seed: %v", err)
	}
	f.Close()
	// AUDIT #6: this seed IS the node's money — it controls every mined reward AND the OBX
	// leg of swaps. It was never surfaced for backup, so losing the datadir silently lost all
	// rewards. Tell the operator LOUDLY, once, at creation.
	log.Printf("============================================================")
	log.Printf("NEW MINER WALLET CREATED at %s", seedPath)
	log.Printf("THIS FILE CONTROLS ALL YOUR MINING REWARDS AND SWAP FUNDS.")
	log.Printf("BACK IT UP NOW (copy the file somewhere safe). If you lose this")
	log.Printf("datadir without a backup, your rewards are GONE — unrecoverable.")
	log.Printf("============================================================")
	return seed
}

// guardPrototypePoW refuses to start on the weak prototype PoW backend.
//
// AUDIT FIX: the pure-Go `vm-randomx-style` backend (pkg/pow/backend_vm.go) has
// near-zero memory-hardness and is for tests/devnets only. A value-bearing node
// MUST run the KAT-verified canonical RandomX backend, which is now the DEFAULT
// build (the prototype is only selected by the explicit `-tags protopow`). If the
// prototype backend is somehow active we log a LOUD warning and abort, unless the
// operator explicitly opts in via OBX_ALLOW_PROTOTYPE_POW=1 (devnets / load tests).
func guardPrototypePoW() {
	const prototypeBackend = "vm-randomx-style"
	if pow.BackendName != prototypeBackend {
		return
	}
	log.Printf("########################################################################")
	log.Printf("# SECURITY WARNING: active PoW backend is the PROTOTYPE %q,", pow.BackendName)
	log.Printf("# which has NEAR-ZERO memory-hardness and is INSECURE for any real chain.")
	log.Printf("# Rebuild with the canonical RandomX backend: plain `go build` (no tags),")
	log.Printf("# or `make` / `./build.sh`. The prototype is ONLY built with -tags protopow.")
	log.Printf("########################################################################")
	if os.Getenv("OBX_ALLOW_PROTOTYPE_POW") != "1" {
		log.Fatalf("refusing to start on prototype PoW backend; set OBX_ALLOW_PROTOTYPE_POW=1 to override (devnets only)")
	}
	log.Printf("OBX_ALLOW_PROTOTYPE_POW=1 set — continuing on INSECURE prototype PoW backend")
}

func defaultDataDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".obscura")
}
