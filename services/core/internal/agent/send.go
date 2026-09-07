package agent

import (
	"fmt"
	"sort"
	"strings"
)

// SendPolicy decides who a guest's send() call may address and, together with a
// Deliverer, defines what a target name means. It is a deliberate, explicit
// policy rather than an invented default: the host ABI has a send() function but
// no opinion about who may be named, and guessing one would be worse than having
// none (see SendFunc).
//
// A name is a deployment id on this node. A permitted target resolves, through
// the Deliverer, to the inbox of another agent deployed on the same node; a
// name that is not permitted, or not deployed, is refused. Nothing off-node is
// addressable: this is a local inter-agent primitive, not a network egress.
//
// SECURE DEFAULT: the zero SendPolicy (Enabled false, empty Allow) refuses every
// send, identical to a nil SendFunc. Inter-agent send is off unless an operator
// turns it on AND names an allowlist; there is no permissive default and no
// "allow all" shortcut. A SendPolicy with Enabled true but an empty Allow still
// refuses everything, so flipping the enable flag alone opens nothing.
type SendPolicy struct {
	// Enabled turns inter-agent send on. When false (the default) every send is
	// refused regardless of Allow.
	Enabled bool
	// Allow is the set of target names (deployment ids) a guest may address. An
	// empty Allow refuses every send even when Enabled is true: the operator must
	// name who may be addressed. There is deliberately no wildcard.
	Allow []string
}

// Permits reports whether the policy allows addressing target. It enforces the
// secure default: a disabled policy, or an empty allowlist, permits nothing.
func (p SendPolicy) Permits(target string) bool {
	if !p.Enabled {
		return false
	}
	for _, name := range p.Allow {
		if name == target {
			return true
		}
	}
	return false
}

// permittedList returns the sorted allowlist for error messages, so a refusal
// can say what would have been permitted.
func (p SendPolicy) permittedList() string {
	if !p.Enabled || len(p.Allow) == 0 {
		return "(none)"
	}
	names := append([]string(nil), p.Allow...)
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// Deliverer resolves a permitted target name to a real recipient and delivers a
// payload to it. It is the "what a name means" half of the policy: SendPolicy
// says who may be named, and the Deliverer turns a permitted name into a
// delivery (e.g. an append to the recipient agent's inbox that the agentapi
// Manager owns). A target that is permitted by policy but does not resolve to a
// live recipient is a delivery error, surfaced to the guest's stderr.
type Deliverer interface {
	// Deliver hands payload to the recipient named by target. It returns an error
	// when the target does not resolve to a deliverable recipient; the caller
	// (hostSend) records the attempt either way and reports the error to stderr.
	Deliver(target string, payload []byte) error
}

// DelivererFunc adapts a plain function to the Deliverer interface.
type DelivererFunc func(target string, payload []byte) error

// Deliver calls the underlying function.
func (f DelivererFunc) Deliver(target string, payload []byte) error { return f(target, payload) }

// NewSendFunc builds a SendFunc from a policy and a deliverer. A permitted
// target is handed to the deliverer and its result returned; a non-permitted
// target returns an error (which hostSend surfaces to the guest's stderr,
// recording the attempt either way).
//
// It returns nil when the policy permits nothing (disabled or empty allowlist),
// which is the secure default: a nil SendFunc is exactly what Config.Send
// treats as "refuse every send and say so". A caller can therefore build a
// SendFunc unconditionally from configuration and get the safe posture for free
// when the operator has not opted in. A nil deliverer under an otherwise
// permissive policy is a programming error and yields a SendFunc that refuses
// every send with an explanatory error rather than a permissive one.
func NewSendFunc(policy SendPolicy, deliverer Deliverer) SendFunc {
	if !policy.Enabled || len(policy.Allow) == 0 {
		// Nothing is permitted: fall back to the nil SendFunc, which refuses
		// every send. This keeps the default posture identical to today.
		return nil
	}
	return func(target string, payload []byte) error {
		if !policy.Permits(target) {
			return fmt.Errorf("target %q is not permitted by the send policy (allowed: %s)", target, policy.permittedList())
		}
		if deliverer == nil {
			return fmt.Errorf("target %q is permitted but no delivery is configured", target)
		}
		return deliverer.Deliver(target, payload)
	}
}
