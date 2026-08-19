package session

import "testing"

func TestReplayWindowAcceptsForwardProgress(t *testing.T) {
	var w ReplayWindow
	for counter := range uint64(200) {
		if !w.Accept(counter) {
			t.Fatalf("counter %d was rejected on first sight", counter)
		}
	}
	// Counter 0 has to work, since it is the first packet of every session.
	var fresh ReplayWindow
	if !fresh.Accept(0) {
		t.Error("counter 0 was rejected")
	}
}

func TestReplayWindowRejectsRepeats(t *testing.T) {
	var w ReplayWindow
	for _, counter := range []uint64{5, 6, 7} {
		if !w.Accept(counter) {
			t.Fatalf("counter %d was rejected on first sight", counter)
		}
	}
	for _, counter := range []uint64{5, 6, 7} {
		if w.Accept(counter) {
			t.Errorf("counter %d was accepted twice", counter)
		}
	}
}

func TestReplayWindowAcceptsReordering(t *testing.T) {
	var w ReplayWindow
	if !w.Accept(100) {
		t.Fatal("counter 100 was rejected")
	}

	// Stragglers inside the window are ordinary UDP behaviour, not an attack.
	for _, counter := range []uint64{99, 98, 90, 100 - WindowSize + 1} {
		if !w.Accept(counter) {
			t.Errorf("counter %d was rejected, but it is inside the window", counter)
		}
		if w.Accept(counter) {
			t.Errorf("counter %d was accepted twice", counter)
		}
	}
}

func TestReplayWindowRejectsAncientPackets(t *testing.T) {
	var w ReplayWindow
	if !w.Accept(1000) {
		t.Fatal("counter 1000 was rejected")
	}
	for _, counter := range []uint64{0, 1000 - WindowSize, 1000 - WindowSize - 1} {
		if w.Accept(counter) {
			t.Errorf("counter %d was accepted, but it is older than the window", counter)
		}
	}
}

func TestReplayWindowHandlesABigJump(t *testing.T) {
	var w ReplayWindow
	if !w.Accept(1) {
		t.Fatal("counter 1 was rejected")
	}
	// A long silence, then traffic resumes far ahead. Nothing before the new
	// window can be judged any more, so it all has to be refused.
	if !w.Accept(1_000_000) {
		t.Fatal("counter 1000000 was rejected")
	}
	if w.Accept(1) {
		t.Error("a counter from before the jump was accepted")
	}
	if !w.Accept(1_000_000 - 1) {
		t.Error("a counter just behind the jump was rejected")
	}
}
