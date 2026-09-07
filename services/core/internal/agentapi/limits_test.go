package agentapi

import (
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/agent"
)

// TestADeployerCannotChooseItsOwnResourceCeiling
//
// THE ATTACK. MaxMemoryPages and MaxRunTimeMS come from the DEPLOYER's request
// (server.go reads l.GetMaxMemoryPages() straight off the wire).
// normalizeLimits fills in a default when a field is zero, and that is all it
// does: it never bounds a field from above. agent.ResourceLimits.Validate
// allows up to 65536 pages, which is 4 GiB, and puts no ceiling on MaxRunTime
// at all - only a negative value is rejected.
//
// So a deployer asks for 65536 pages and 24 hours, and one agent run may hold
// 4 GiB of a node's memory for a day. The defaults being safe (16 MiB, 5s) is
// not the point: they are defaults, and the caller overrides them upward.
//
// The metering charge makes it worse rather than better. It is a FLAT price per
// run (Manager.charge submits m.meter.Price, once), so the price a deployer pays
// is the same whether the module returns immediately or consumes the whole
// ceiling it chose for itself. Paying once buys as much of the node as the
// request asked for.
func TestADeployerCannotChooseItsOwnResourceCeiling(t *testing.T) {
	cases := []struct {
		name string
		in   agent.ResourceLimits
		want agent.ResourceLimits
	}{
		{
			name: "4 GiB of memory is clamped to the ceiling",
			in:   agent.ResourceLimits{MaxMemoryPages: 65536, MaxRunTime: time.Second},
			want: agent.ResourceLimits{MaxMemoryPages: MaxAllowedMemoryPages, MaxRunTime: time.Second},
		},
		{
			name: "a 24-hour run time is clamped to the ceiling",
			in:   agent.ResourceLimits{MaxMemoryPages: 64, MaxRunTime: 24 * time.Hour},
			want: agent.ResourceLimits{MaxMemoryPages: 64, MaxRunTime: MaxAllowedRunTime},
		},
		{
			name: "both at once",
			in:   agent.ResourceLimits{MaxMemoryPages: 65536, MaxRunTime: 24 * time.Hour},
			want: agent.ResourceLimits{MaxMemoryPages: MaxAllowedMemoryPages, MaxRunTime: MaxAllowedRunTime},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeLimits(tc.in)
			if got.MaxMemoryPages != tc.want.MaxMemoryPages {
				t.Fatalf("MaxMemoryPages = %d, want %d: a deployer set its own memory ceiling",
					got.MaxMemoryPages, tc.want.MaxMemoryPages)
			}
			if got.MaxRunTime != tc.want.MaxRunTime {
				t.Fatalf("MaxRunTime = %s, want %s: a deployer set its own time ceiling",
					got.MaxRunTime, tc.want.MaxRunTime)
			}
		})
	}
}

// TestAModestRequestIsHonoured. The ceiling must clamp, not overwrite: a
// deployer asking for less than the ceiling gets what it asked for, and a
// deployer asking for nothing gets the default.
func TestAModestRequestIsHonoured(t *testing.T) {
	got := normalizeLimits(agent.ResourceLimits{MaxMemoryPages: 32, MaxRunTime: 2 * time.Second})
	if got.MaxMemoryPages != 32 {
		t.Fatalf("MaxMemoryPages = %d, want the requested 32", got.MaxMemoryPages)
	}
	if got.MaxRunTime != 2*time.Second {
		t.Fatalf("MaxRunTime = %s, want the requested 2s", got.MaxRunTime)
	}

	def := normalizeLimits(agent.ResourceLimits{})
	if def.MaxMemoryPages != agent.DefaultMemoryLimits.MaxMemoryPages {
		t.Fatalf("an unset request got %d pages, want the default %d",
			def.MaxMemoryPages, agent.DefaultMemoryLimits.MaxMemoryPages)
	}
	if def.MaxRunTime != agent.DefaultMemoryLimits.MaxRunTime {
		t.Fatalf("an unset request got %s, want the default %s",
			def.MaxRunTime, agent.DefaultMemoryLimits.MaxRunTime)
	}
}

// TestTheCeilingIsBelowWhatValidateAllows. agent.Validate's 65536 pages is the
// wasm format's own maximum, not a policy: it is what the runtime can express,
// and this package is where the policy for an UNTRUSTED submitted module lives.
// A ceiling equal to Validate's would be no ceiling.
func TestTheCeilingIsBelowWhatValidateAllows(t *testing.T) {
	if MaxAllowedMemoryPages >= 65536 {
		t.Fatalf("MaxAllowedMemoryPages is %d, which is the wasm maximum: that is not a policy",
			MaxAllowedMemoryPages)
	}
	if MaxAllowedRunTime <= 0 {
		t.Fatal("MaxAllowedRunTime must be positive; zero means no deadline")
	}
	// The clamped limits must still satisfy the runtime's own validation, or a
	// clamp would turn a deployable request into an error.
	clamped := normalizeLimits(agent.ResourceLimits{MaxMemoryPages: 65536, MaxRunTime: 24 * time.Hour})
	if err := clamped.Validate(); err != nil {
		t.Fatalf("clamped limits do not validate: %v", err)
	}
}

// TestTheDeploymentRecordAlsoClamps. Limits() rebuilds the limits from the
// PERSISTED record on every later run, so a clamp applied only at deploy time
// would be undone by a restart or by any path that re-reads the record. A
// deployment stored before the ceiling existed carries the old numbers.
func TestTheDeploymentRecordAlsoClamps(t *testing.T) {
	d := Deployment{
		MaxMemoryPages: 65536,
		MaxRunTimeMS:   uint64((24 * time.Hour).Milliseconds()),
	}
	got := d.Limits()
	if got.MaxMemoryPages != MaxAllowedMemoryPages {
		t.Fatalf("a stored deployment reconstructed %d pages, want the ceiling %d: a record "+
			"written before the ceiling existed would still get 4 GiB",
			got.MaxMemoryPages, MaxAllowedMemoryPages)
	}
	if got.MaxRunTime != MaxAllowedRunTime {
		t.Fatalf("a stored deployment reconstructed %s, want the ceiling %s",
			got.MaxRunTime, MaxAllowedRunTime)
	}
}

// TestTheInboxIsBounded
//
// An inbox lives in the Manager and OUTLIVES the run that filled it, which
// makes it a worse place for unbounded growth than the sender's own send log:
// the bytes stayed resident until the recipient deployment was removed. A
// delivered payload may be a megabyte, so a few thousand of them is the node.
//
// deliver is called only for a target the operator's allowlist already permits,
// so the sender is trusted - which bounds the blast radius but not the memory.
func TestTheInboxIsBounded(t *testing.T) {
	m := &Manager{
		records:    map[string]Deployment{"peer-1": {}},
		inbox:      make(map[string][]agent.Message),
		inboxBytes: make(map[string]int),
	}

	payload := make([]byte, 64<<10) // 64 KiB, a plausible message
	delivered, refused := 0, 0
	for i := 0; i < MaxInboxMessages*4; i++ {
		if err := m.deliver("peer-1", payload); err != nil {
			refused++
			continue
		}
		delivered++
	}

	if refused == 0 {
		t.Fatalf("all %d deliveries were accepted; the inbox is unbounded", delivered)
	}
	held := m.Inbox("peer-1")
	if len(held) > MaxInboxMessages {
		t.Fatalf("the inbox holds %d messages, cap is %d", len(held), MaxInboxMessages)
	}
	var bytes int
	for _, msg := range held {
		bytes += len(msg.Payload)
	}
	if bytes > MaxInboxBytes {
		t.Fatalf("the inbox holds %d bytes, limit is %d", bytes, MaxInboxBytes)
	}
}

// TestDrainingAnInboxFreesItForMore is the other half of a bounded queue.
// Without a drain, a full inbox is full for good: nothing could free it, so a
// permitted sender could permanently stop delivery to a recipient, and the
// bound would have traded an unbounded-memory bug for a permanent-refusal one.
// Inbox only peeks, which is why DrainInbox had to exist.
func TestDrainingAnInboxFreesItForMore(t *testing.T) {
	m := &Manager{
		records:    map[string]Deployment{"peer-1": {}},
		inbox:      make(map[string][]agent.Message),
		inboxBytes: make(map[string]int),
	}

	payload := make([]byte, 64<<10)
	for {
		if err := m.deliver("peer-1", payload); err != nil {
			break
		}
	}
	// Full. A peek must not free it.
	before := len(m.Inbox("peer-1"))
	if err := m.deliver("peer-1", payload); err == nil {
		t.Fatal("a full inbox accepted a delivery after only a peek")
	}
	if now := len(m.Inbox("peer-1")); now != before {
		t.Fatalf("Inbox consumed messages: %d then %d; it is documented as a non-destructive read",
			before, now)
	}

	drained := m.DrainInbox("peer-1")
	if len(drained) != before {
		t.Fatalf("DrainInbox returned %d messages, want the %d held", len(drained), before)
	}
	if left := len(m.Inbox("peer-1")); left != 0 {
		t.Fatalf("%d messages left after draining", left)
	}
	if err := m.deliver("peer-1", payload); err != nil {
		t.Fatalf("delivery still refused after draining: %v", err)
	}
}

// TestDeliveryToAnUnknownAgentIsStillRefused: the bound must not have widened
// what a name can address. Nothing off-node, and nothing not deployed here.
func TestDeliveryToAnUnknownAgentIsStillRefused(t *testing.T) {
	m := &Manager{
		records:    map[string]Deployment{"peer-1": {}},
		inbox:      make(map[string][]agent.Message),
		inboxBytes: make(map[string]int),
	}
	if err := m.deliver("not-deployed", []byte("hi")); err == nil {
		t.Fatal("delivery to an agent that is not deployed on this node was accepted")
	}
}
