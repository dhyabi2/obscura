package p2p

import "testing"

func TestIsRoutable(t *testing.T) {
	cases := map[string]bool{
		"1.2.3.4:18080":   true,
		"127.0.0.1:18080": true, // loopback ok (local devnets)
		"0.0.0.0:18080":   false, // unspecified — the bug we fixed
		"[::]:18080":      false,
		"1.2.3.4:0":       false, // zero port
		":18080":          false, // no host
		"garbage":         false,
	}
	for a, want := range cases {
		if got := isRoutable(a); got != want {
			t.Errorf("isRoutable(%q)=%v want %v", a, got, want)
		}
	}
}

// TestSelfDiscovery: a node learns its public address only when ≥minSelfDiscoveryGroups
// DISTINCT reporter /16 networks agree on the observed IP (anti-poison), combined with its
// own listen port. The threshold was raised to 4 (audit: a 2-group Sybil could forge it).
func TestSelfDiscovery(t *testing.T) {
	n := &Node{addr: "0.0.0.0:18080"}
	// reporters from the SAME /16 (198.51.100.x) count as ONE network — must NOT adopt.
	n.learnExternalFromPeer("203.0.113.7:40001", "198.51.100.4:5000")
	n.learnExternalFromPeer("203.0.113.7:55002", "198.51.100.9:6000")
	if n.getAdvertise() != "" {
		t.Fatalf("adopted from a single reporter network (poisoning vector): %q", n.getAdvertise())
	}
	// distinct networks agreeing, but still BELOW the 4-group threshold → must NOT adopt.
	n.learnExternalFromPeer("203.0.113.7:7777", "192.0.2.55:7000")   // network 2
	n.learnExternalFromPeer("203.0.113.7:8888", "203.0.113.9:8000")  // network 3
	if n.getAdvertise() != "" {
		t.Fatalf("adopted with only 3 distinct networks (threshold is %d): %q", minSelfDiscoveryGroups, n.getAdvertise())
	}
	// the FOURTH distinct reporter network agreeing → adopt.
	n.learnExternalFromPeer("203.0.113.7:9999", "100.64.7.7:9000") // network 4
	if want := "203.0.113.7:18080"; n.getAdvertise() != want {
		t.Fatalf("self-discovered %q, want %q", n.getAdvertise(), want)
	}
	// loopback / unspecified observations must NOT be adopted, even across networks.
	m := &Node{addr: "0.0.0.0:18080"}
	m.learnExternalFromPeer("127.0.0.1:1", "198.51.100.4:5000")
	m.learnExternalFromPeer("127.0.0.1:2", "192.0.2.55:7000")
	if m.getAdvertise() != "" {
		t.Fatalf("adopted a loopback observation: %q", m.getAdvertise())
	}
}

// TestAdvertiseOverride: an explicit SetAdvertise pins the address and disables auto-learn.
func TestAdvertiseOverride(t *testing.T) {
	n := &Node{addr: "0.0.0.0:18080"}
	n.SetAdvertise("5.6.7.8:18080")
	n.learnExternalFromPeer("203.0.113.7:1", "198.51.100.4:5000")
	n.learnExternalFromPeer("203.0.113.7:2", "192.0.2.55:7000")
	if n.getAdvertise() != "5.6.7.8:18080" {
		t.Fatalf("explicit advertise overridden by auto-learn: %q", n.getAdvertise())
	}
}
