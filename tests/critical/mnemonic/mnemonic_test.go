// Package mnemonic_test covers the seed word-phrase codec (Block 26): round-trip,
// checksum typo detection, word-count/word validation, wordlist uniqueness, and
// that the decoded seed derives the same wallet.
package mnemonic_test

import (
	"bytes"
	"crypto/rand"
	"strings"
	"testing"

	"obscura/pkg/mnemonic"
	"obscura/pkg/wallet"
)

func TestRoundTrip(t *testing.T) {
	for i := 0; i < 50; i++ {
		seed := make([]byte, 32)
		rand.Read(seed)
		phrase, err := mnemonic.Encode(seed)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		if n := len(strings.Fields(phrase)); n != 24 {
			t.Fatalf("32-byte seed produced %d words, want 24", n)
		}
		got, err := mnemonic.Decode(phrase)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !bytes.Equal(got, seed) {
			t.Fatalf("round-trip mismatch:\n seed=%x\n got =%x", seed, got)
		}
	}
}

func TestDecodedSeedDerivesSameWallet(t *testing.T) {
	seed := make([]byte, 32)
	rand.Read(seed)
	phrase, _ := mnemonic.Encode(seed)
	got, err := mnemonic.Decode(phrase)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(wallet.FromSeed(got).AddressBytes(), wallet.FromSeed(seed).AddressBytes()) {
		t.Fatal("wallet from decoded phrase differs from the original seed")
	}
}

func TestChecksumDetectsTypo(t *testing.T) {
	seed := make([]byte, 32)
	rand.Read(seed)
	phrase, _ := mnemonic.Encode(seed)
	words := strings.Fields(phrase)

	// replace one word with a DIFFERENT valid wordlist word; the checksum should
	// almost always reject it (probability of a silent pass ≈ 1/256 per word).
	all := mnemonic.Words()
	replacement := all[0]
	if words[5] == replacement {
		replacement = all[1]
	}
	words[5] = replacement
	if _, err := mnemonic.Decode(strings.Join(words, " ")); err == nil {
		t.Fatal("a single-word typo passed the checksum")
	}
}

func TestUnknownWordRejected(t *testing.T) {
	seed := make([]byte, 32)
	rand.Read(seed)
	phrase, _ := mnemonic.Encode(seed)
	words := strings.Fields(phrase)
	words[0] = "xxnotaword"
	if _, err := mnemonic.Decode(strings.Join(words, " ")); err == nil {
		t.Fatal("an unknown word was accepted")
	}
}

func TestBadWordCount(t *testing.T) {
	if _, err := mnemonic.Decode("bada bada bada"); err == nil { // 3 words
		t.Fatal("invalid word count accepted")
	}
}

func TestWordlistUniqueAnd2048(t *testing.T) {
	w := mnemonic.Words()
	if len(w) != 2048 {
		t.Fatalf("wordlist has %d entries, want 2048", len(w))
	}
	seen := make(map[string]bool, 2048)
	for _, x := range w {
		if seen[x] {
			t.Fatalf("duplicate word %q in wordlist", x)
		}
		seen[x] = true
	}
}

func TestEncodeRejectsBadEntropy(t *testing.T) {
	if _, err := mnemonic.Encode(make([]byte, 13)); err == nil {
		t.Fatal("encode accepted 13-byte entropy")
	}
}
