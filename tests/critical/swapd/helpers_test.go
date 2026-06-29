package swapd_test

import "filippo.io/edwards25519"

// pt returns the public point s·B for a scalar — a shared helper for the swap tests.
func pt(s *edwards25519.Scalar) *edwards25519.Point { return new(edwards25519.Point).ScalarBaseMult(s) }
