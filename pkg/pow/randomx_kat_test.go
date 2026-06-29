//go:build !protopow

package pow

import (
	"encoding/hex"
	"testing"
)

// TestCanonicalRandomXVectors proves the wired backend produces byte-identical
// output to reference Monero RandomX, using the official known-answer vectors
// (key + input -> hash). HashSeed(seed,input) maps directly onto RandomX's
// cache.Init(key) + CalculateHash(input), so the seed IS the RandomX key.
func TestCanonicalRandomXVectors(t *testing.T) {
	if BackendName != "randomx-canonical" {
		t.Fatalf("expected canonical RandomX backend, got %q", BackendName)
	}
	vectors := []struct{ key, input, want string }{
		{"test key 000", "This is a test", "639183aae1bf4c9a35884cb46b09cad9175f04efd7684e7262a0ac1c2f0b4e3f"},
		{"test key 000", "Lorem ipsum dolor sit amet", "300a0adb47603dedb42228ccb2b211104f4da45af709cd7547cd049e9489c969"},
		{"test key 000", "sed do eiusmod tempor incididunt ut labore et dolore magna aliqua", "c36d4ed4191e617309867ed66a443be4075014e2b061bcdaf9ce7b721d2b77a8"},
	}
	for _, v := range vectors {
		got := HashSeed([]byte(v.key), []byte(v.input))
		if hex.EncodeToString(got[:]) != v.want {
			t.Fatalf("RandomX mismatch:\n key=%q input=%q\n got =%x\n want=%s", v.key, v.input, got, v.want)
		}
	}
}

func TestCanonicalDeterministic(t *testing.T) {
	a := Hash([]byte("obscura-canonical-determinism"))
	if Hash([]byte("obscura-canonical-determinism")) != a {
		t.Fatal("canonical RandomX hash not deterministic")
	}
}
