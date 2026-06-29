// Package miner grinds the proof-of-work nonce for block templates.
package miner

import (
	"context"

	"obscura/pkg/block"
	"obscura/pkg/config"
	"obscura/pkg/pow"
)

// Mine grinds the nonce under the epoch-0 PoW seed (correct for early blocks;
// callers on deep chains should use MineSeed with the per-epoch seed).
func Mine(ctx context.Context, b *block.Block, startNonce uint64) bool {
	return MineSeed(ctx, b, config.PoWGenesisSeed, startNonce)
}

// MineSeed grinds the nonce of b's header under the given per-epoch PoW seed until
// it satisfies the difficulty, or the context is cancelled. Returns true if a
// valid nonce was found. startNonce lets callers partition the search space.
func MineSeed(ctx context.Context, b *block.Block, seed []byte, startNonce uint64) bool {
	diff := b.Header.Difficulty
	nonce := startNonce
	for {
		select {
		case <-ctx.Done():
			return false
		default:
		}
		b.Header.Nonce = nonce
		if pow.Meets(b.Header.PoWHashSeed(seed), diff) {
			return true
		}
		nonce++
		if nonce == startNonce-1 {
			return false // wrapped around
		}
	}
}
