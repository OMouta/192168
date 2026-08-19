package session

// ReplayWindow tracks which packet counters have already been seen on a link.
//
// UDP reorders, so refusing anything that is not strictly newer would drop
// perfectly good packets. Accepting anything at all would let an attacker
// resend a captured packet forever. The window is the middle: a packet is fine
// if it is newer than everything seen so far, or if it is inside the last
// WindowSize counters and has not appeared before.
//
// A ReplayWindow is not safe for concurrent use. One link, one reader.
type ReplayWindow struct {
	// highest is the largest counter accepted so far.
	highest uint64
	// seen is a bitmap of the WindowSize counters ending at highest. Bit 0 is
	// highest itself, bit n is highest-n.
	seen uint64
	// started is false until the first packet, so counter 0 is still usable.
	started bool
}

// WindowSize is how far behind the newest packet a straggler can arrive and
// still be accepted.
const WindowSize = 64

// Accept reports whether a counter is new, and records it when it is. A false
// means the packet is a replay or too old to judge, and the caller drops it.
//
// Only call this after the packet has been decrypted. A counter from an
// unauthenticated packet is attacker controlled, and feeding it here would let
// anyone slide the window forward and lock out real traffic.
func (w *ReplayWindow) Accept(counter uint64) bool {
	if !w.started {
		w.started = true
		w.highest = counter
		w.seen = 1
		return true
	}

	if counter > w.highest {
		shift := counter - w.highest
		if shift >= WindowSize {
			// The jump is bigger than the window, so nothing old survives.
			w.seen = 0
		} else {
			w.seen <<= shift
		}
		w.seen |= 1
		w.highest = counter
		return true
	}

	behind := w.highest - counter
	if behind >= WindowSize {
		return false
	}
	if w.seen&(1<<behind) != 0 {
		return false
	}
	w.seen |= 1 << behind
	return true
}
