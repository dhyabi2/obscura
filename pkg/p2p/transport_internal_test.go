package p2p

import "testing"

func TestIsOnion(t *testing.T) {
	if !isOnion("abcdefghijklmnop.onion:18080") {
		t.Fatal(".onion not recognized")
	}
	if isOnion("1.2.3.4:18080") {
		t.Fatal("clearnet wrongly classified as onion")
	}
}

func TestMaybeAddAddrOnionOnlyFilter(t *testing.T) {
	n := &Node{book: NewAddrBook(""), onionOnly: true}
	n.maybeAddAddr("1.2.3.4:18080")                  // clearnet -> dropped
	n.maybeAddAddr("abcdefghijklmnop.onion:18080")   // onion    -> kept
	got := n.book.Sample(10)
	if len(got) != 1 || got[0] != "abcdefghijklmnop.onion:18080" {
		t.Fatalf("onion-only filter wrong, book=%v", got)
	}
}

func TestMaybeAddAddrClearnetMode(t *testing.T) {
	n := &Node{book: NewAddrBook(""), onionOnly: false}
	n.maybeAddAddr("1.2.3.4:18080") // clearnet kept when not in Tor mode
	if len(n.book.Sample(10)) != 1 {
		t.Fatal("clearnet addr should be kept in clearnet mode")
	}
}

func TestSetTransportFields(t *testing.T) {
	n := &Node{book: NewAddrBook("")}
	n.SetTransport(nil, "myonion.onion:18080", true)
	if n.advertiseAddr != "myonion.onion:18080" || !n.onionOnly {
		t.Fatal("SetTransport did not set advertise/onionOnly")
	}
}
