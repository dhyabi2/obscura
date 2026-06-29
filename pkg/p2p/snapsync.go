package p2p

import (
	"encoding/binary"
	"os"
	"strconv"
	"time"
)

// Snapshot fast-sync (P2P transport for the chain's verified snapshot import). A node that is
// FAR behind a peer (e.g. fresh or long-restarted) fast-forwards by downloading the peer's
// verified transfer snapshot instead of re-verifying every historical block (which is ~1 block/s
// dominated by the class-group accumulator). The chain side is already built + tested:
// chain.ExportTransferSnapshot (serve) and chain.VerifyAndImportSnapshot (receive, PoW-verified,
// adversarial reject-tests green). This file only adds the wire transport + a bounded trigger.
//
// SAFETY: this is a SEPARATE code path from steady-state block sync (msgTip/msgGetBlk/msgBlock),
// which is unchanged. It fires ONLY when a node is > snapSyncGap behind, at most one transfer in
// flight, with a hard byte cap + timeout (anti-DoS), and the import itself rejects any tampered/
// fake-PoW/foreign snapshot. On import failure it simply falls back to normal block sync.

const (
	snapChunkSize   = maxMsgBytes - 64    // payload bytes per chunk (room for the 16B header)
	snapMaxChunks   = 8192                 // bound on chunk count (anti-DoS)
	snapMaxBytes    = 1 << 30              // 1 GiB hard cap on a reassembled snapshot (anti-DoS)
	snapXferTimeout = 90 * time.Second     // a stalled transfer is abandoned (then block-sync resumes)
)

// snapSyncGap: trigger snapshot fast-sync when this many blocks behind a peer. Env-overridable
// (OBX_SNAP_SYNC_GAP) for devnet/testing; default 200 (below that, block-sync is fine).
var snapSyncGap uint64 = 200

func init() {
	if v := os.Getenv("OBX_SNAP_SYNC_GAP"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil && n > 0 {
			snapSyncGap = n
		}
	}
}

// snapXfer is the in-flight INBOUND snapshot reassembly state (one at a time per node).
type snapXfer struct {
	from     string   // peer remote addr we requested from (only this peer's chunks are accepted)
	total    uint32   // total chunks (0 until the first chunk sets it)
	height   uint64   // snapshot height (informational)
	chunks   [][]byte // received chunks indexed by seq
	got      int      // distinct chunks received
	bytes    int      // bytes accumulated (bounded by snapMaxBytes)
	deadline time.Time
}

// maybeRequestSnapshot starts a snapshot fast-sync from p if we are far behind and none is in
// flight. Called from the msgTip handler.
func (n *Node) maybeRequestSnapshot(p *peer, peerH, ourH uint64) {
	if peerH <= ourH+snapSyncGap {
		return
	}
	n.snapMu.Lock()
	if n.snapXfer != nil && time.Now().Before(n.snapXfer.deadline) {
		n.snapMu.Unlock()
		return // a transfer is already in progress
	}
	n.snapXfer = &snapXfer{from: p.conn.RemoteAddr().String(), deadline: time.Now().Add(snapXferTimeout)}
	n.snapMu.Unlock()
	p2pLog("snapshot fast-sync: %d behind %s (peer %d / ours %d), requesting snapshot", peerH-ourH, p.conn.RemoteAddr(), peerH, ourH)
	_ = n.send(p, msgGetSnapshot, nil)
}

// serveSnapshot answers a peer's msgGetSnapshot by streaming the verified transfer snapshot in
// chunks. Run in its own goroutine (the export can be large); msgGetSnapshot is rate-limited so a
// peer cannot spam expensive exports.
func (n *Node) serveSnapshot(p *peer) {
	data, h, err := n.chain.ExportTransferSnapshot()
	if err != nil || len(data) == 0 {
		p2pLog("serve snapshot to %s -> none (%v)", p.conn.RemoteAddr(), err)
		return
	}
	total := (len(data) + snapChunkSize - 1) / snapChunkSize
	if total == 0 || total > snapMaxChunks {
		return
	}
	p2pLog("serve snapshot h=%d (%d bytes, %d chunks) to %s", h, len(data), total, p.conn.RemoteAddr())
	for i := 0; i < total; i++ {
		lo := i * snapChunkSize
		hi := lo + snapChunkSize
		if hi > len(data) {
			hi = len(data)
		}
		hdr := make([]byte, 16)
		binary.BigEndian.PutUint32(hdr[0:], uint32(i))
		binary.BigEndian.PutUint32(hdr[4:], uint32(total))
		binary.BigEndian.PutUint64(hdr[8:], h)
		if err := n.send(p, msgSnapshot, append(hdr, data[lo:hi]...)); err != nil {
			return // peer gone; abandon
		}
	}
}

// recvSnapshotChunk reassembles inbound snapshot chunks; on completion it verifies+imports.
func (n *Node) recvSnapshotChunk(p *peer, payload []byte) {
	if len(payload) < 16 {
		return
	}
	seq := binary.BigEndian.Uint32(payload[0:])
	total := binary.BigEndian.Uint32(payload[4:])
	height := binary.BigEndian.Uint64(payload[8:])
	chunk := payload[16:]
	remote := p.conn.RemoteAddr().String()

	n.snapMu.Lock()
	x := n.snapXfer
	// only accept chunks for an in-flight transfer we requested from THIS peer, not expired.
	if x == nil || x.from != remote || time.Now().After(x.deadline) {
		n.snapMu.Unlock()
		return
	}
	if total == 0 || total > snapMaxChunks || seq >= total {
		n.snapMu.Unlock()
		return
	}
	if x.total == 0 {
		x.total = total
		x.height = height
		x.chunks = make([][]byte, total)
	}
	if x.total != total {
		n.snapMu.Unlock()
		return // total changed mid-transfer: malformed
	}
	if x.chunks[seq] == nil {
		x.bytes += len(chunk)
		if x.bytes > snapMaxBytes {
			n.snapXfer = nil // anti-DoS: abandon an over-large transfer
			n.snapMu.Unlock()
			return
		}
		x.chunks[seq] = append([]byte(nil), chunk...)
		x.got++
	}
	complete := x.got == int(x.total)
	var full []byte
	if complete {
		for _, c := range x.chunks {
			full = append(full, c...)
		}
		n.snapXfer = nil
	}
	n.snapMu.Unlock()

	if !complete {
		return
	}
	h, err := n.chain.VerifyAndImportSnapshot(full)
	if err != nil {
		p2pLog("snapshot import from %s REJECTED: %v (falling back to block sync)", remote, err)
		return
	}
	p2pLog("snapshot IMPORTED from %s -> tip now %d", remote, h)
	_ = n.send(p, msgGetTip, nil) // resume normal block sync for the recent tail
}
