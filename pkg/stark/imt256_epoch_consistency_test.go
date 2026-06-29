package stark

import (
	"math/rand"
	"testing"
)

// TestEpochRootAfterMatchesAppendMine: the header-prediction RootAfter must EXACTLY equal
// the result of actually Appending — across epoch rollovers, empty batches, and
// starting-from-full — or the committed header root diverges from applied state.
func TestEpochRootAfterMatchesAppendMine(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for trial := 0; trial < 200; trial++ {
		depth := 1 + r.Intn(3) // cap 2..8 → frequent rollovers
		e := NewEpochIMT(depth)
		// random pre-fill
		for i := 0; i < r.Intn(20); i++ {
			e.Append(randNode256(r))
		}
		// a random batch (incl. empty and boundary-crossing sizes)
		batch := make([]Node256, r.Intn(12))
		for i := range batch {
			batch[i] = randNode256(r)
		}
		predicted := e.RootAfter(batch)
		for _, leaf := range batch {
			e.Append(leaf)
		}
		if predicted != e.CurrentRoot() {
			t.Fatalf("trial %d depth %d batch %d: RootAfter != applied root", trial, depth, len(batch))
		}
	}
}
