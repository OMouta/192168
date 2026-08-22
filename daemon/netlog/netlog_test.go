package netlog

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// recorder builds one writing its summaries into a buffer the test can read.
func recorder(t *testing.T) (*Recorder, *bytes.Buffer) {
	t.Helper()
	var summaries bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&summaries, nil))
	r := New(log, filepath.Join(t.TempDir(), "packets.log"))
	t.Cleanup(func() { r.Close() })
	return r, &summaries
}

// lines splits a log buffer into decoded records.
func lines(t *testing.T, buffer *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buffer.String()), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		out = append(out, record)
	}
	return out
}

func TestSummaryNamesOnlyWhatMoved(t *testing.T) {
	r, summaries := recorder(t)

	r.Sent()
	r.Sent()
	r.Received()
	r.Drop(NoSession, "deviceId", "dev_1")
	r.Drop(NoSession, "deviceId", "dev_1")
	r.Drop(AdapterFull)

	r.log.Info("traffic", r.totals().attrs(totals{})...)

	record := lines(t, summaries)[0]
	for key, want := range map[string]float64{
		"sent": 2, "received": 1, "dropped": 3, "noSession": 2, "adapterFull": 1,
	} {
		if got, ok := record[key]; !ok || got != want {
			t.Errorf("%s = %v, want %v", key, got, want)
		}
	}

	// A reason that never fired would bury the ones that did.
	for _, absent := range []string{"replayed", "undecryptable", "noPath", "queueFull"} {
		if _, present := record[absent]; present {
			t.Errorf("%s is in the summary but never happened", absent)
		}
	}
}

// A summary reports the change since the last one, not the running total, so a
// problem that stopped stops being reported.
func TestSummaryReportsTheChangeSinceTheLastOne(t *testing.T) {
	r, summaries := recorder(t)

	r.Sent()
	r.Drop(Replayed)
	first := r.totals()

	r.Sent()
	r.Sent()
	r.log.Info("traffic", r.totals().attrs(first)...)

	record := lines(t, summaries)[0]
	if got := record["sent"]; got != float64(2) {
		t.Errorf("sent = %v, want 2", got)
	}
	if _, present := record["replayed"]; present {
		t.Error("a drop from before the last summary was reported again")
	}
}

func TestRunSaysNothingWhileIdle(t *testing.T) {
	r, summaries := recorder(t)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go r.Run(ctx)

	// Long enough for a tick to have happened if the interval were short; the
	// point is that an idle daemon writes nothing regardless.
	time.Sleep(50 * time.Millisecond)
	if summaries.Len() != 0 {
		t.Errorf("an idle recorder wrote %q", summaries.String())
	}
}

func TestDetailOnlyReachesTheFileWhileItIsOn(t *testing.T) {
	r, _ := recorder(t)

	// Off: the file should not even exist.
	r.Drop(NoPeer, "to", "10.69.0.9")
	r.Detail("something", "key", "value")
	if _, err := os.Stat(r.path); !os.IsNotExist(err) {
		t.Fatalf("the packet log exists before it was turned on: %v", err)
	}
	if got := r.drops[NoPeer].Load(); got != 1 {
		t.Errorf("drops = %d, want 1 counted even with the log off", got)
	}

	if err := r.SetEnabled(true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	r.Drop(NoPeer, "to", "10.69.0.9")
	r.Shouted(netip.MustParseAddr("224.0.2.60"), 2, 96)
	r.Overheard(netip.MustParseAddr("10.69.0.1"), netip.MustParseAddr("224.0.2.60"), 96)

	// Closing flushes and releases the handle so the file can be read back.
	if err := r.SetEnabled(false); err != nil {
		t.Fatalf("SetEnabled(false): %v", err)
	}

	body, err := os.ReadFile(r.path)
	if err != nil {
		t.Fatalf("read the packet log: %v", err)
	}
	written := string(body)
	for _, want := range []string{"noPeer", "10.69.0.9", "224.0.2.60", "10.69.0.1"} {
		if !strings.Contains(written, want) {
			t.Errorf("the packet log does not mention %q:\n%s", want, written)
		}
	}
	// Exactly three: one drop and the two discovery packets. The drop from
	// before it was turned on is not in here.
	if got := strings.Count(strings.TrimSpace(written), "\n") + 1; got != 3 {
		t.Errorf("the packet log has %d lines, want 3:\n%s", got, written)
	}
}

func TestClearEmptiesTheLogWhetherOrNotItIsOpen(t *testing.T) {
	for _, leaveOpen := range []bool{true, false} {
		t.Run(map[bool]string{true: "open", false: "closed"}[leaveOpen], func(t *testing.T) {
			r, _ := recorder(t)

			if err := r.SetEnabled(true); err != nil {
				t.Fatalf("SetEnabled: %v", err)
			}
			r.Drop(Replayed, "deviceId", "dev_1")
			if !leaveOpen {
				if err := r.SetEnabled(false); err != nil {
					t.Fatalf("SetEnabled(false): %v", err)
				}
			}

			if err := r.Clear(); err != nil {
				t.Fatalf("Clear: %v", err)
			}

			body, err := os.ReadFile(r.path)
			if leaveOpen {
				// Still held open, so it is emptied rather than removed.
				if err != nil {
					t.Fatalf("read the packet log: %v", err)
				}
				if len(body) != 0 {
					t.Errorf("the packet log still holds %q", body)
				}
				return
			}
			if !os.IsNotExist(err) {
				t.Errorf("a closed packet log survived being cleared: %v, %q", err, body)
			}
		})
	}
}

// The packet path calls these before there is anywhere to record to, so a nil
// recorder has to be as good as a real one rather than a crash on the first
// dropped packet.
func TestNilRecorderIsUsable(t *testing.T) {
	var r *Recorder

	r.Sent()
	r.Received()
	r.Drop(NoPath, "deviceId", "dev_1")
	r.Shouted(netip.MustParseAddr("224.0.2.60"), 1, 32)
	r.Overheard(netip.MustParseAddr("10.69.0.1"), netip.MustParseAddr("224.0.2.60"), 32)
	r.Detail("something")
	r.Run(t.Context())

	if r.Enabled() {
		t.Error("a nil recorder claims to be recording")
	}
	if err := r.SetEnabled(true); err != nil {
		t.Errorf("SetEnabled on a nil recorder: %v", err)
	}
	if err := r.Clear(); err != nil {
		t.Errorf("Clear on a nil recorder: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Errorf("Close on a nil recorder: %v", err)
	}
}

func TestEveryReasonHasAName(t *testing.T) {
	for i := range reasonCount {
		if reasonNames[i] == "" {
			t.Errorf("reason %d has no name, so it would be counted into an empty key", i)
		}
	}
}
