// obscura-loadgen drives a sustained confidential-transaction load against a
// running Obscura network and records a metrics time series for analysis.
//
// node0 mines coinbase rewards to builder wallet[0]. Each tick the coordinator
// syncs new blocks (scanning them into every builder wallet), tops up the other
// builder wallets from wallet[0], then fans out: each of N builder wallets, in
// its own goroutine, builds confidential self-send transactions
// (wallet.CreateTransaction — the sound transparent path) and submits them
// round-robin to the nodes' /submittx. Parallel building (one wallet per core)
// raises client throughput so the 4 nodes are actually saturated, and a
// confirmed self-send yields two owned outputs (dest+change), growing each
// wallet's spendable pool. It stops at the target confirmed-tx count.
//
// It samples /status on every node each tick and writes a CSV (one row/tick):
// elapsed, per-node tip height, confirmed txs, submitted txs, per-node mempool,
// accumulator size, difficulty, windowed TPS, avg tx-build ms, avg submit ms,
// total spendable pool. scripts/loadtest/chart.py renders a chart + a
// degradation analysis.
package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"obscura/pkg/block"
	"obscura/pkg/config"
	"obscura/pkg/tx"
	"obscura/pkg/wallet"
)

type heightView uint64

func (h heightView) Height() uint64 { return uint64(h) }

var nodes []string

func main() {
	var (
		seedHex   = flag.String("seed", "0baddecaf0baddecaf0baddecaf0baddecaf0baddecaf0baddecaf0badde0001", "base 32-byte wallet seed (hex); builder i uses seed^i")
		nodesCSV  = flag.String("nodes", "http://127.0.0.1:28081,http://127.0.0.1:28082,http://127.0.0.1:28083,http://127.0.0.1:28084", "comma-separated node RPC base URLs")
		target    = flag.Uint64("target", 100000, "stop after this many CONFIRMED transactions")
		out       = flag.String("out", "loadtest_metrics.csv", "metrics CSV output path")
		tickMS    = flag.Int("tick", 1000, "metrics/generation tick in milliseconds")
		mpTarget  = flag.Int("mempool-target", 4000, "keep node[0] mempool around this depth")
		builders  = flag.Int("builders", 6, "number of parallel builder wallets")
		fee       = flag.Uint64("fee", 500_000_000, "fee per tx (atomic)")
		fund      = flag.Uint64("fund", 5_000_000_000_000, "chunk wallet[0] sends to top up each other builder (atomic)")
		perCap    = flag.Int("per-cap", 64, "max txs each builder submits per tick (keeps ticks short)")
		maxMin    = flag.Int("max-minutes", 180, "safety time cap (minutes)")
		cbMat     = flag.Uint64("coinbase-maturity", 1, "MUST match the network's --coinbase-maturity")
		printAddr = flag.Bool("print-address", false, "print builder[0] address and exit")
		submitAll = flag.Bool("submit-all", false, "submit round-robin to ALL nodes (distributed flood) instead of only the miner")
	)
	flag.Parse()
	config.CoinbaseMaturity = *cbMat // match the network or spendable funds appear immature

	base, err := hex.DecodeString(*seedHex)
	if err != nil || len(base) != 32 {
		log.Fatalf("seed must be 32-byte hex")
	}
	mkWallet := func(i int) *wallet.Wallet {
		s := append([]byte(nil), base...)
		s[0] ^= byte(i)
		s[31] ^= byte(i * 7)
		return wallet.FromSeed(s)
	}
	w0 := mkWallet(0)
	if *printAddr {
		fmt.Println(w0.Address().String())
		return
	}
	nodes = splitCSV(*nodesCSV)
	if len(nodes) == 0 {
		log.Fatal("no nodes")
	}
	// Submit to the NON-mining followers only: node[0] mines (its CPU is pegged
	// grinding PoW), so validating submits there is slow. Followers validate fast
	// and gossip txs to the miner's mempool. Blocks/status are still read from
	// node[0]. Falls back to all nodes if there is only one.
	// Submit straight to the MINER (node[0]) so txs land in the mining mempool
	// immediately — relaying via a follower means Dandelion++ stem delay keeps them
	// out of the miner's mempool. Read blocks from a follower so reads don't
	// compete with the miner's PoW + submit validation.
	// Both submit and chain-sync use the MINER (node[0]): it is the authoritative
	// tip and txs land in its mempool immediately (no Dandelion stem delay). Under
	// fast mining, followers can lag propagation, so reading their tip would stall
	// the loadgen — read from the miner instead.
	submitNodes := nodes[:1]
	if *submitAll {
		submitNodes = nodes // distribute submissions round-robin across all droplets
	}
	syncNode := nodes[0]
	if len(nodes) > 1 {
		syncNode = nodes[1] // read chain from an idle follower; the miner now throttles
		// empty blocks so followers stay synced and serve fast block reads.
	}
	ws := make([]*wallet.Wallet, *builders)
	for i := range ws {
		ws[i] = mkWallet(i)
	}
	log.Printf("loadgen: %d nodes, %d builder wallets, target=%d confirmed txs", len(nodes), *builders, *target)

	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	fmt.Fprintln(f, "elapsed_s,tip0,tip1,tip2,tip3,confirmed,submitted,mp0,mp1,mp2,mp3,acc_size,difficulty,tps,build_ms,submit_ms,pool")

	hc := &http.Client{Timeout: 20 * time.Second}
	var (
		scanned   uint64
		confirmed uint64
		submitted int64
		prevConf  uint64
		prevT     = time.Now()
		start     = time.Now()
	)
	tick := time.Duration(*tickMS) * time.Millisecond

	for {
		loopStart := time.Now()
		st0 := status(hc, syncNode)
		tip := st0.Height

		// --- sync: fetch each new block once, scan into every builder wallet.
		// Cap per tick so the loop always exits to write a metrics row even if
		// block production temporarily outpaces scanning. ---
		for n := 0; scanned < tip && n < 2000; n++ {
			b, err := getBlock(hc, syncNode, scanned+1)
			if err != nil {
				break
			}
			for _, w := range ws {
				w.ScanBlock(b)
			}
			if len(b.Txs) > 0 {
				confirmed += uint64(len(b.Txs) - 1)
			}
			scanned++
		}
		for _, w := range ws {
			pruneAndDedup(w)
		}

		view := heightView(tip)
		// --- top up the other builders from wallet[0] (several chunks each so they
		// have enough seed outputs to self-split into a deep spendable pool) ---
		for i := 1; i < len(ws); i++ {
			for fundN := 0; fundN < 4 && len(ws[i].SpendableOutputs(tip+1)) < 40; fundN++ {
				t, err := w0.CreateTransaction(view, ws[i].Address(), *fund, *fee)
				if err != nil {
					break
				}
				if submitTx(hc, submitNodes[0], t) {
					atomic.AddInt64(&submitted, 1)
				} else {
					w0.ReleaseReservation(t)
					break
				}
			}
		}

		// --- parallel build+submit across builder wallets ---
		room := *mpTarget - st0.MempoolSize
		if room < 0 {
			room = 0
		}
		per := room/len(ws) + 1
		if per > *perCap {
			per = *perCap
		}
		var buildUs, submitUs, builtN, subN int64
		var wg sync.WaitGroup
		for i := range ws {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				w := ws[i]
				// FAN-OUT: split each output into two ~equal reusable halves. This
				// grows the spendable pool exponentially (1→2→4→…), so the mempool
				// fills and blocks run full — throughput becomes build/submit-limited
				// instead of capped at the per-coinbase confirm rate. Largest first.
				pool := w.SpendableOutputs(tip + 1)
				sort.Slice(pool, func(a, b int) bool { return pool[a].Amount > pool[b].Amount })
				k := 0
				for _, o := range pool {
					if k >= per || confirmed >= *target {
						break
					}
					half := (o.Amount - *fee) / 2
					if half <= *fee { // too small to split further — leave as-is
						continue
					}
					tb := time.Now()
					t, err := w.CreateTransactionFrom(view, o, w.Address(), half, *fee)
					if err != nil {
						continue
					}
					atomic.AddInt64(&buildUs, time.Since(tb).Microseconds())
					atomic.AddInt64(&builtN, 1)
					node := submitNodes[(i+k)%len(submitNodes)]
					ts := time.Now()
					ok := submitTx(hc, node, t)
					atomic.AddInt64(&submitUs, time.Since(ts).Microseconds())
					atomic.AddInt64(&subN, 1)
					if ok {
						atomic.AddInt64(&submitted, 1)
					} else {
						w.ReleaseReservation(t)
					}
					k++
				}
			}(i)
		}
		wg.Wait()

		// --- metrics row ---
		now := time.Now()
		elapsed := now.Sub(start).Seconds()
		dt := now.Sub(prevT).Seconds()
		tps := 0.0
		if dt > 0 {
			tps = float64(confirmed-prevConf) / dt
		}
		prevConf, prevT = confirmed, now
		tips := make([]uint64, 4)
		mps := make([]int, 4)
		for i := 0; i < 4 && i < len(nodes); i++ {
			s := status(hc, nodes[i])
			tips[i] = s.Height
			mps[i] = s.MempoolSize
		}
		buildMs, submitMs := 0.0, 0.0
		if builtN > 0 {
			buildMs = float64(buildUs) / float64(builtN) / 1000
		}
		if subN > 0 {
			submitMs = float64(submitUs) / float64(subN) / 1000
		}
		pool := 0
		for _, w := range ws {
			pool += len(w.SpendableOutputs(tip + 1))
		}
		fmt.Fprintf(f, "%.1f,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%.1f,%.3f,%.3f,%d\n",
			elapsed, tips[0], tips[1], tips[2], tips[3], confirmed, atomic.LoadInt64(&submitted),
			mps[0], mps[1], mps[2], mps[3], st0.AccSize, st0.Difficulty, tps, buildMs, submitMs, pool)
		f.Sync()
		log.Printf("t=%.0fs h=%d conf=%d sub=%d mp=%v acc=%d diff=%d tps=%.0f build=%.1fms pool=%d",
			elapsed, tip, confirmed, atomic.LoadInt64(&submitted), mps, st0.AccSize, st0.Difficulty, tps, buildMs, pool)

		if confirmed >= *target {
			log.Printf("DONE: %d confirmed txs in %.0fs (%.1f tx/s)", confirmed, elapsed, float64(confirmed)/elapsed)
			return
		}
		if now.Sub(start) > time.Duration(*maxMin)*time.Minute {
			log.Printf("TIME CAP at %d confirmed txs", confirmed)
			return
		}
		if d := tick - time.Since(loopStart); d > 0 {
			time.Sleep(d)
		}
	}
}

type statusResp struct {
	Height      uint64 `json:"height"`
	Difficulty  uint64 `json:"difficulty"`
	AccSize     uint64 `json:"accumulator_size"`
	MempoolSize int    `json:"mempool_size"`
}

func status(hc *http.Client, base string) statusResp {
	var s statusResp
	_ = getJSON(hc, base+"/status", &s)
	return s
}

func getBlock(hc *http.Client, base string, h uint64) (*block.Block, error) {
	var r struct {
		Block string `json:"block"`
	}
	if err := getJSON(hc, fmt.Sprintf("%s/block?height=%d", base, h), &r); err != nil {
		return nil, err
	}
	if r.Block == "" {
		return nil, fmt.Errorf("empty")
	}
	raw, err := hex.DecodeString(r.Block)
	if err != nil {
		return nil, err
	}
	return block.DeserializeBlock(raw)
}

var lastSubmitErr atomic.Value

func submitTx(hc *http.Client, base string, t *tx.Transaction) bool {
	body, _ := json.Marshal(map[string]string{"tx": hex.EncodeToString(t.Serialize())})
	resp, err := hc.Post(base+"/submittx", "application/json", bytes.NewReader(body))
	if err != nil {
		lastSubmitErr.Store(err.Error())
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		io.Copy(io.Discard, resp.Body)
		return true
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
	lastSubmitErr.Store(fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(b)))
	return false
}

func getJSON(hc *http.Client, url string, v any) error {
	resp, err := hc.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// pruneAndDedup drops confirmed-spent outputs AND collapses duplicate entries
// for the same one-time key (the wallet can record a coin more than once during
// scanning; duplicates would otherwise defeat per-output reservation and cause
// double-spend submissions). Keeping one entry per key makes the wallet's own
// reservation sufficient to never re-select an in-flight output.
func pruneAndDedup(w *wallet.Wallet) {
	seen := make(map[string]bool, len(w.Outputs))
	kept := w.Outputs[:0]
	for _, o := range w.Outputs {
		if o.Spent {
			continue
		}
		k := hexstr(o.Out.OneTimeKey)
		if seen[k] {
			continue
		}
		seen[k] = true
		kept = append(kept, o)
	}
	w.Outputs = kept
}

func hexstr(b []byte) string { return hex.EncodeToString(b) }

func splitCSV(s string) []string {
	var out []string
	cur := ""
	for _, c := range s {
		if c == ',' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
		} else {
			cur += string(c)
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
