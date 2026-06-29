package swapnet_test

import (
	"testing"
	"time"

	"obscura/pkg/swapd"
	"obscura/pkg/swapnet"
	"obscura/pkg/swapsession"
)

// TestActiveSessionsReflectsRunningSession starts a TAKER session whose
// counterparty never replies (a memTransport that captures Init but answers
// nothing), so the session sits alive at PhaseInit. ActiveSessions() must report
// exactly that session: correct id, taker role, init phase, the agreed amounts,
// and a step list whose ONLY done step is "init".
func TestActiveSessionsReflectsRunningSession(t *testing.T) {
	sc := newSharedChain(t)
	nano := swapd.NewMockNano()

	tr := &memTransport{} // captures outbound Init, delivers no reply -> session stalls alive
	coord, err := swapnet.New(swapnet.Config{
		Transport: tr,
		Taker:     takerCaps{s: sc, nano: nano},
		Timeout:   30 * time.Second, // long enough that the session is still running when we snapshot
		Fee:       testFee,
	})
	if err != nil {
		t.Fatalf("coord: %v", err)
	}
	defer coord.Stop()

	// Before any session, the snapshot is empty.
	if got := coord.ActiveSessions(); len(got) != 0 {
		t.Fatalf("expected 0 active sessions before Take, got %d", len(got))
	}

	sess, err := coord.Take("maker-peer", testOBX, testXNO, testFee)
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	id := sess.ID()

	// Poll briefly for the driver goroutine to have bound its state (it sends Init,
	// then blocks on recv — the session stays registered the whole time).
	var view swapnet.SessionView
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		views := coord.ActiveSessions()
		if len(views) == 1 {
			view = views[0]
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if view.ID == "" {
		t.Fatal("ActiveSessions never reported the running session")
	}
	wantID := hexID(id)
	if view.ID != wantID {
		t.Fatalf("session id = %s, want %s", view.ID, wantID)
	}
	if view.Role != string(swapsession.RoleTaker) {
		t.Fatalf("role = %q, want taker", view.Role)
	}
	if view.Phase != string(swapsession.PhaseInit) {
		t.Fatalf("phase = %q, want init", view.Phase)
	}
	if view.OBXAmount != testOBX || view.XNOAmount == nil || view.XNOAmount.Cmp(testXNO) != 0 {
		t.Fatalf("amounts = %d/%v, want %d/%s", view.OBXAmount, view.XNOAmount, testOBX, testXNO)
	}
	if view.Updated == 0 {
		t.Fatal("updated timestamp not set")
	}
	// Step list: init done, the rest not.
	wantDone := map[string]bool{"init": true}
	for _, st := range view.Steps {
		if st.Done != wantDone[st.Name] {
			t.Fatalf("step %q done=%v, want %v", st.Name, st.Done, wantDone[st.Name])
		}
	}
	// init must be present and done.
	var sawInit bool
	for _, st := range view.Steps {
		if st.Name == "init" {
			sawInit = true
			if !st.Done {
				t.Fatal("init step should be done")
			}
		}
	}
	if !sawInit {
		t.Fatal("step list missing init")
	}
}

// hexID renders a 32-byte swap id as lowercase hex (matching SessionView.ID).
func hexID(id [32]byte) string {
	const hexdig = "0123456789abcdef"
	out := make([]byte, 64)
	for i, b := range id {
		out[i*2] = hexdig[b>>4]
		out[i*2+1] = hexdig[b&0x0f]
	}
	return string(out)
}
