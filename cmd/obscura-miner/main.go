// Command obscura-miner is a standalone CPU miner. It mines against any node's
// RPC using the /blocktemplate and /submitblock endpoints, so mining is fully
// decoupled from running a node — point it at a node, give it a payout address,
// and it grinds.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"obscura/pkg/config"
	"obscura/pkg/miner"
	"obscura/pkg/rpc"
)

func main() {
	var (
		node    = flag.String("node", fmt.Sprintf("http://127.0.0.1:%d", config.DefaultRPCPort), "node RPC URL")
		address = flag.String("address", "", "payout address (Base58 or hex) — required")
	)
	flag.Parse()
	if *address == "" {
		log.Fatal("--address is required (where block rewards are paid)")
	}
	cl, err := rpc.NewClient(*node)
	if err != nil {
		log.Fatalf("rpc: %v", err)
	}
	log.Printf("%s miner → node %s | payout %s…", config.CoinName, *node, short(*address))

	for {
		mined, height, err := mineOnce(cl, *address)
		if err != nil {
			log.Printf("retry: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}
		if mined {
			log.Printf("⛏  mined and submitted block %d", height)
		}
	}
}

// mineOnce fetches a template, grinds it (cancelling if the chain advances under
// us so we don't waste work on a stale template), and submits a solved block.
// Returns (mined, acceptedHeight, err).
func mineOnce(cl *rpc.Client, address string) (bool, uint64, error) {
	tmpl, seed, _, err := cl.BlockTemplate(address)
	if err != nil {
		return false, 0, err
	}
	startH := tmpl.Header.Height

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// stop grinding if a new block arrives (someone else won this height)
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if cl.Height() >= startH {
					cancel()
					return
				}
			}
		}
	}()

	found := miner.MineSeed(ctx, tmpl, seed, 0)
	close(done)
	if !found {
		return false, 0, nil // template went stale; loop will fetch a fresh one
	}
	h, err := cl.SubmitBlock(tmpl)
	if err != nil {
		// lost the race / rejected — not fatal, just try again
		return false, 0, nil
	}
	return true, h, nil
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
