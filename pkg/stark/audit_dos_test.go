package stark

import (
	"math/rand"
	"testing"
)

// AUDIT item 9: a verifier fed malformed / truncated / random proof bytes must
// return an error or false — never panic (a panic in consensus = remote DoS).
func TestAuditProofDecodeNoPanic(t *testing.T) {
	// a real proof to corrupt
	imt := buildSpend256Tree(3, 1, Felt(1), Felt(2), Felt(3), 7)
	pf, _ := ProveSpend256(Felt(1), Felt(2), Felt(3), imt.PathFor(1), 3, imt.Root(), nil, airQueries)
	good, _ := MarshalProof(pf)

	r := rand.New(rand.NewSource(1))
	check := func(b []byte) {
		defer func() {
			if rec := recover(); rec != nil {
				t.Fatalf("PANIC on malformed proof input: %v", rec)
			}
		}()
		p, err := UnmarshalProof(b)
		if err == nil && p != nil {
			// even a decodable-but-garbage proof must not panic in verify
			_ = VerifySpend256(Felt(1), Felt(2), imt.Root(), nil, 3, p, airQueries)
		}
	}

	// random blobs
	for i := 0; i < 2000; i++ {
		n := r.Intn(len(good) + 1)
		b := make([]byte, n)
		r.Read(b)
		check(b)
	}
	// truncations of a valid proof
	for n := 0; n < len(good); n += 37 {
		check(good[:n])
	}
	// single-byte bitflips of a valid proof
	for i := 0; i < 3000; i++ {
		b := append([]byte(nil), good...)
		b[r.Intn(len(b))] ^= byte(1 << uint(r.Intn(8)))
		check(b)
	}
	// empty + nil
	check(nil)
	check([]byte{})
}
