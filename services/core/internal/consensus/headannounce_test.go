package consensus

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// TestAnAnnouncementCanTellTwoChainsApart is what height alone could not do. Two
// nodes both at height 900 looked identical in an announcement even when they
// had committed different blocks or reached different balances, and on an idle
// network the announcement is the only signal that arrives at all.
func TestAnAnnouncementCanTellTwoChainsApart(t *testing.T) {
	eng := blockTimeEngine(t, time.Now)
	eng.mu.Lock()
	eng.headHash = bytes.Repeat([]byte{0x11}, HashSize)
	mine := append([]byte(nil), eng.stateRootLocked()...)
	height := eng.height
	eng.mu.Unlock()

	for _, tc := range []struct {
		name    string
		ann     HeadAnnounce
		wantSay string
	}{
		{
			name:    "a peer that agrees says nothing",
			ann:     HeadAnnounce{NodeID: "peer", Height: height, HeadHash: bytes.Repeat([]byte{0x11}, HashSize), StateRoot: mine},
			wantSay: "",
		},
		{
			name:    "a different chain",
			ann:     HeadAnnounce{NodeID: "peer", Height: height, HeadHash: bytes.Repeat([]byte{0x22}, HashSize), StateRoot: mine},
			wantSay: "DIFFERENT chain",
		},
		{
			name:    "the same chain, a different ledger",
			ann:     HeadAnnounce{NodeID: "peer", Height: height, HeadHash: bytes.Repeat([]byte{0x11}, HashSize), StateRoot: bytes.Repeat([]byte{0x99}, 32)},
			wantSay: "DIFFERENT ledger",
		},
		{
			name:    "a peer at another height is not comparable",
			ann:     HeadAnnounce{NodeID: "peer", Height: height + 1, HeadHash: bytes.Repeat([]byte{0x22}, HashSize)},
			wantSay: "",
		},
		{
			name:    "an old node that sends neither field",
			ann:     HeadAnnounce{NodeID: "peer", Height: height},
			wantSay: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eng.mu.Lock()
			eng.headComplaints = nil
			got := eng.headDisagreementLocked(&tc.ann)
			eng.mu.Unlock()
			if tc.wantSay == "" {
				if got != "" {
					t.Fatalf("expected silence, got %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.wantSay) {
				t.Fatalf("complaint = %q, want it to mention %q", got, tc.wantSay)
			}
		})
	}
}

// TestARepeatingAnnouncementDoesNotRepeatTheLogLine matters because the
// announcement is on a timer and a disagreement does not resolve itself, so an
// unbounded line would bury everything else the node has to say.
func TestARepeatingAnnouncementDoesNotRepeatTheLogLine(t *testing.T) {
	eng := blockTimeEngine(t, time.Now)
	eng.mu.Lock()
	eng.headHash = bytes.Repeat([]byte{0x11}, HashSize)
	height := eng.height
	eng.mu.Unlock()

	ann := HeadAnnounce{NodeID: "peer", Height: height, HeadHash: bytes.Repeat([]byte{0x22}, HashSize)}

	eng.mu.Lock()
	first := eng.headDisagreementLocked(&ann)
	second := eng.headDisagreementLocked(&ann)
	eng.mu.Unlock()

	if first == "" {
		t.Fatal("the first disagreement should be reported")
	}
	if second != "" {
		t.Fatalf("the same disagreement was reported twice: %q", second)
	}

	// A DIFFERENT complaint from the same peer is still worth saying.
	ann.HeadHash = bytes.Repeat([]byte{0x33}, HashSize)
	eng.mu.Lock()
	third := eng.headDisagreementLocked(&ann)
	eng.mu.Unlock()
	if third == "" {
		t.Fatal("a peer moving to a third chain should be reported")
	}
}

// TestANodeDoesNotComplainAboutItself covers the announcement that comes back
// over gossip to its own sender.
func TestANodeDoesNotComplainAboutItself(t *testing.T) {
	eng := blockTimeEngine(t, time.Now)
	eng.mu.Lock()
	defer eng.mu.Unlock()
	got := eng.headDisagreementLocked(&HeadAnnounce{
		NodeID:   eng.selfID,
		Height:   eng.height,
		HeadHash: bytes.Repeat([]byte{0xff}, HashSize),
	})
	if got != "" {
		t.Fatalf("a node complained about its own announcement: %q", got)
	}
}
