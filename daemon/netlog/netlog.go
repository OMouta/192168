// Package netlog is the daemon's account of what happens to packets.
//
// The ordinary log says what the daemon did: adapters came up, links opened,
// groups were joined. It says nothing about the traffic all of that exists to
// carry, so "my friends cannot see my game" arrives with no way to tell whether
// the packets left one machine, reached the other, or were thrown away in
// between. There are about forty places in the peer code where a packet is
// discarded, and until this they were all silent.
//
// Two things cover that, and they are deliberately not the same thing. Counters
// run always, cost one atomic add, and are summarised into the ordinary log
// every half minute: enough to see that packets are being lost and why, on a
// log any user already has. The per-packet detail is a second file the user
// turns on, because a game moves thousands of packets a second and a line for
// each would roll the history away faster than it built up.
package netlog

import (
	"context"
	"log/slog"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/OMouta/192168/daemon/config"
)

// summaryInterval is how often the counters reach the ordinary log. A session
// is minutes long, so this is often enough to show a problem starting and rare
// enough to cost nothing.
const summaryInterval = 30 * time.Second

// detailLimit is the packet log's own rollover, larger than the daemon log's
// because it is written far faster and only while somebody is watching.
const detailLimit = 32 << 20

// Reason is why a packet did not make it. Each one is counted separately,
// because which of them is climbing is the whole diagnosis: a link that never
// opened, a peer that moved, and an adapter that cannot keep up all look like
// packet loss to a game and want completely different answers.
type Reason uint8

const (
	// NoPeer is an outgoing packet for an address in the subnet that nobody in
	// the group holds.
	NoPeer Reason = iota
	// NoSession is a peer whose handshake has not finished.
	NoSession
	// NoPath is a peer with no endpoint to send to.
	NoPath
	// NotIPv4 is anything the overlay does not carry, which in practice is
	// IPv6 that Windows offers the adapter anyway.
	NotIPv4
	// AdapterFull is the virtual adapter's ring backing up because Windows is
	// not draining it.
	AdapterFull
	// QueueFull is the same congestion one step earlier: decrypted packets
	// arriving faster than the adapter accepts them.
	QueueFull
	// Undecryptable is a packet the session keys rejected: forged, corrupted,
	// or from a peer that restarted with keys this side has not been given.
	Undecryptable
	// Replayed is a counter the replay window has already seen.
	Replayed
	// UnknownPeer is a packet from an address no peer sits at, which is what a
	// NAT moving somebody's mapping looks like from here.
	UnknownPeer
	// Unroutable is a forwarded packet whose destination this daemon holds no
	// open link to.
	Unroutable
	// HopsExhausted is a forwarded packet that has gone as far as it may.
	HopsExhausted
	// Malformed is a datagram that is not one of ours at all, which on an open
	// UDP port is mostly scanners.
	Malformed
	// Undeliverable is the real socket refusing the write, which is the
	// network underneath having gone away rather than anything about the peer.
	Undeliverable

	reasonCount
)

// reasonNames double as the attribute key in a summary and the reason on a
// detail line, so the two read the same way.
var reasonNames = [reasonCount]string{
	NoPeer:        "noPeer",
	NoSession:     "noSession",
	NoPath:        "noPath",
	NotIPv4:       "notIPv4",
	AdapterFull:   "adapterFull",
	QueueFull:     "queueFull",
	Undecryptable: "undecryptable",
	Replayed:      "replayed",
	UnknownPeer:   "unknownPeer",
	Unroutable:    "unroutable",
	HopsExhausted: "hopsExhausted",
	Malformed:     "malformed",
	Undeliverable: "undeliverable",
}

func (r Reason) String() string {
	if int(r) >= len(reasonNames) {
		return "unknown"
	}
	return reasonNames[r]
}

// Recorder counts what happens to packets and, when asked, writes down each
// one worth seeing.
//
// The zero value is not usable; New builds one. A nil Recorder is, though:
// every method tolerates it, so code on the packet path does not have to check
// whether recording is set up.
type Recorder struct {
	log  *slog.Logger
	path string

	sent, received     atomic.Uint64
	shouted, overheard atomic.Uint64
	drops              [reasonCount]atomic.Uint64

	// on is read once per packet, so it is kept out from behind the mutex,
	// which is only there for opening and closing the file.
	on atomic.Bool

	mu     sync.Mutex
	file   *config.RollingLog
	detail *slog.Logger
}

// New builds a recorder that summarises into log and, once turned on, writes
// its detail to path.
func New(log *slog.Logger, path string) *Recorder {
	return &Recorder{log: log, path: path}
}

// Enabled reports whether the packet log is on.
func (r *Recorder) Enabled() bool { return r != nil && r.on.Load() }

// SetEnabled opens or closes the packet log.
//
// The file is only held open while it is on. Somebody who turned it on to catch
// a problem and forgot about it is paying for a file that rolls over, which is
// bounded; somebody who never turned it on has no file at all.
func (r *Recorder) SetEnabled(on bool) error {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if on == (r.file != nil) {
		return nil
	}

	if !on {
		// Cleared first, so nothing new arrives at a file that is closing.
		r.on.Store(false)
		file := r.file
		r.file, r.detail = nil, nil
		return file.Close()
	}

	file, err := config.OpenRolling(r.path, detailLimit)
	if err != nil {
		return err
	}
	r.file = file
	r.detail = slog.New(slog.NewJSONHandler(file, nil))
	r.on.Store(true)
	return nil
}

// Clear empties the packet log, whether or not it is currently on.
func (r *Recorder) Clear() error {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	file := r.file
	r.mu.Unlock()

	if file != nil {
		return file.Clear()
	}

	// Nothing is holding it open, so whatever an earlier session left behind
	// is an ordinary file.
	return config.RemoveLog(r.path)
}

// Close puts the packet log down.
func (r *Recorder) Close() error { return r.SetEnabled(false) }

// Sent counts a packet handed to a peer.
func (r *Recorder) Sent() {
	if r != nil {
		r.sent.Add(1)
	}
}

// Received counts a packet handed to the adapter.
func (r *Recorder) Received() {
	if r != nil {
		r.received.Add(1)
	}
}

// Drop counts a packet that did not make it, and writes down the particulars
// when the packet log is on.
//
// The count is the part that runs always. It is what turns "it does not work"
// into "eleven thousand packets went nowhere because the link was never open",
// which is a different conversation.
func (r *Recorder) Drop(reason Reason, attrs ...any) {
	if r == nil {
		return
	}
	if int(reason) < len(r.drops) {
		r.drops[reason].Add(1)
	}
	if !r.on.Load() {
		return
	}
	r.Detail("dropped a packet", append([]any{"reason", reason.String()}, attrs...)...)
}

// Shouted records a packet addressed to the whole LAN going out to the group.
//
// Discovery traffic gets a line each because there are a handful of them a
// second rather than thousands, and because they are the ones somebody is
// usually asking about: a game that nobody else can see is either not sending
// these or not receiving them, and one line at each end says which.
func (r *Recorder) Shouted(destination netip.Addr, peers int, bytes int) {
	if r == nil {
		return
	}
	r.shouted.Add(1)
	if r.on.Load() {
		r.Detail("replicated a packet for the whole network", "to", destination.String(), "peers", peers, "bytes", bytes)
	}
}

// Overheard records one arriving from the group.
func (r *Recorder) Overheard(from, destination netip.Addr, bytes int) {
	if r == nil {
		return
	}
	r.overheard.Add(1)
	if r.on.Load() {
		r.Detail("a packet for the whole network arrived", "from", from.String(), "to", destination.String(), "bytes", bytes)
	}
}

// Detail writes one line to the packet log, and nothing at all when it is off.
func (r *Recorder) Detail(msg string, attrs ...any) {
	if r == nil || !r.on.Load() {
		return
	}

	r.mu.Lock()
	detail := r.detail
	r.mu.Unlock()

	if detail != nil {
		detail.Info(msg, attrs...)
	}
}

// Run writes a summary into the ordinary log until ctx is cancelled.
func (r *Recorder) Run(ctx context.Context) {
	if r == nil {
		return
	}

	ticker := time.NewTicker(summaryInterval)
	defer ticker.Stop()

	var last totals
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current := r.totals()
			// A daemon sitting idle between games says nothing, so the log
			// stays worth reading over a long session.
			if current == last {
				continue
			}
			r.log.Info("traffic", current.attrs(last)...)
			last = current
		}
	}
}

// totals is one reading of every counter, so a summary can report the change
// since the last one as well as the running total.
type totals struct {
	sent, received     uint64
	shouted, overheard uint64
	drops              [reasonCount]uint64
}

func (r *Recorder) totals() totals {
	out := totals{
		sent:      r.sent.Load(),
		received:  r.received.Load(),
		shouted:   r.shouted.Load(),
		overheard: r.overheard.Load(),
	}
	for i := range r.drops {
		out.drops[i] = r.drops[i].Load()
	}
	return out
}

// attrs builds the summary line. Only the counters that have moved since the
// last one are named, because a line listing eleven kinds of failure that are
// all zero buries the one that is not.
func (t totals) attrs(since totals) []any {
	attrs := []any{
		"sent", t.sent - since.sent,
		"received", t.received - since.received,
	}
	if moved := t.shouted - since.shouted; moved > 0 {
		attrs = append(attrs, "replicated", moved)
	}
	if moved := t.overheard - since.overheard; moved > 0 {
		attrs = append(attrs, "arrivedForEveryone", moved)
	}

	var dropped uint64
	for i, count := range t.drops {
		moved := count - since.drops[i]
		if moved == 0 {
			continue
		}
		dropped += moved
		attrs = append(attrs, Reason(i).String(), moved)
	}
	if dropped > 0 {
		// In front of the breakdown, so the number that says whether to keep
		// reading comes first.
		attrs = append([]any{"dropped", dropped}, attrs...)
	}
	return attrs
}
