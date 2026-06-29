package p2p

import "testing"

// TestEclipseResistance: an attacker flooding one /16 cannot dominate the book OR a
// node's dial set; the honest minority stays well-represented.
func TestEclipseResistance(t *testing.T) {
	ab := NewAddrBook("")
	// attacker floods 5000 addresses from one /16 (203.0.x.x)
	for i := 0; i < 5000; i++ {
		ab.Add("203.0." + itoa(i/256) + "." + itoa(i%256) + ":18080")
	}
	// honest peers across many distinct /16s
	honest := map[string]bool{}
	for i := 0; i < 40; i++ {
		a := "10." + itoa(i) + ".5.5:18080"
		ab.Add(a)
		honest[a] = true
	}
	// 1) the attacker /16 is capped, not the whole book
	if c := ab.groups["4:"+string([]byte{203, 0})]; c > maxPerGroup {
		t.Fatalf("attacker group not capped: %d > %d", c, maxPerGroup)
	}
	// 2) a dial sample of 32 spans many groups — attacker can't dominate it
	s := ab.Sample(32)
	attackerInSample, groups := 0, map[string]bool{}
	for _, a := range s {
		groups[ipGroup(a)] = true
		if ipGroup(a) == "4:"+string([]byte{203, 0}) {
			attackerInSample++
		}
	}
	if attackerInSample > 2 {
		t.Fatalf("attacker occupies %d/%d of dial set — eclipse risk", attackerInSample, len(s))
	}
	if len(groups) < 10 {
		t.Fatalf("dial set spans only %d groups — not diverse", len(groups))
	}
	t.Logf("dial set: %d peers across %d groups, attacker=%d (defended)", len(s), len(groups), attackerInSample)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
