// Package light implements SPV light-client verification: it validates a chain
// of block HEADERS (proof-of-work, linkage, difficulty retargeting, timestamps,
// cumulative work) WITHOUT downloading or executing full blocks, and verifies
// transaction inclusion via Merkle proofs. This lets wallets confirm the best
// chain and that a transaction is in it at a tiny fraction of full-node cost.
//
// Trust model (standard SPV): a light client trusts that the most-work valid
// header chain reflects the honest majority; it does NOT re-validate
// transactions (no inflation/double-spend checking) — that is the full node's
// job. Wallet output scanning still needs per-output data (view-tags, a
// documented refinement, speed that up).
package light

import (
	"errors"
	"math/big"
	"sort"
	"time"

	"obscura/pkg/block"
	"obscura/pkg/config"
	"obscura/pkg/consensus"
	"obscura/pkg/pow"
)

// VerifyHeaderChain checks an ordered slice of headers (genesis first) and
// returns the tip hash, height, and cumulative work. It verifies, for each
// non-genesis header: linkage to its parent, the LWMA-expected difficulty, that
// the PoW meets that difficulty, and timestamp sanity (median-time-past +
// bounded future drift). The genesis header must match expectGenesisID (the
// light client's hardcoded trust root).
func VerifyHeaderChain(headers []block.Header, expectGenesisID [32]byte) (tip [32]byte, height uint64, work *big.Int, err error) {
	if len(headers) == 0 {
		return tip, 0, nil, errors.New("light: empty header chain")
	}
	if headers[0].Height != 0 || headers[0].ID() != expectGenesisID {
		return tip, 0, nil, errors.New("light: genesis mismatch")
	}
	work = new(big.Int)
	now := time.Now().Unix()
	for i := range headers {
		h := headers[i]
		if h.Height != uint64(i) {
			return tip, 0, nil, errors.New("light: non-contiguous height")
		}
		if i > 0 {
			if h.PrevHash != headers[i-1].ID() {
				return tip, 0, nil, errors.New("light: broken parent link")
			}
			if h.Difficulty == 0 {
				return tip, 0, nil, errors.New("light: zero difficulty")
			}
			ts, df := window(headers[:i])
			if h.Difficulty != consensus.NextDifficulty(ts, df) {
				return tip, 0, nil, errors.New("light: wrong difficulty")
			}
			if h.Timestamp <= medianTimePast(headers[:i]) {
				return tip, 0, nil, errors.New("light: timestamp <= MTP")
			}
			if h.Timestamp > now+config.MaxFutureDriftSeconds {
				return tip, 0, nil, errors.New("light: timestamp too far in future")
			}
			// per-epoch PoW seed: epoch 0 uses the constant, later epochs use the
			// id of the seed-height header (present in this genesis-first slice).
			seed := config.PoWGenesisSeed
			if sh := config.PoWSeedHeight(h.Height); sh > 0 && sh < uint64(len(headers)) {
				id := headers[sh].ID()
				seed = id[:]
			}
			if !pow.Meets(h.PoWHashSeed(seed), h.Difficulty) {
				return tip, 0, nil, errors.New("light: insufficient proof of work")
			}
		}
		work.Add(work, new(big.Int).SetUint64(h.Difficulty))
	}
	last := headers[len(headers)-1]
	return last.ID(), last.Height, work, nil
}

// VerifyInclusion confirms a transaction (by txid) is committed by a verified
// header's MerkleRoot via its inclusion branch.
func VerifyInclusion(header block.Header, txid [32]byte, steps []block.MerkleStep) bool {
	return block.VerifyMerkleProof(txid, steps, header.MerkleRoot)
}

func window(prev []block.Header) ([]int64, []uint64) {
	n := config.DifficultyWindow + 1
	start := 0
	if len(prev) > n {
		start = len(prev) - n
	}
	var ts []int64
	var df []uint64
	for _, h := range prev[start:] {
		ts = append(ts, h.Timestamp)
		df = append(df, h.Difficulty)
	}
	return ts, df
}

func medianTimePast(prev []block.Header) int64 {
	const n = 11
	start := 0
	if len(prev) > n {
		start = len(prev) - n
	}
	var tsv []int64
	for _, h := range prev[start:] {
		tsv = append(tsv, h.Timestamp)
	}
	if len(tsv) == 0 {
		return 0
	}
	sort.Slice(tsv, func(i, j int) bool { return tsv[i] < tsv[j] })
	return tsv[len(tsv)/2]
}
