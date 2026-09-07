package consensus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/libp2p/go-libp2p/core/peer"
)

// The gossip guard: what a node checks about a message before pubsub relays it.
//
// THE PROBLEM. gossipsub with no registered topic validator accepts any message
// published to a subscribed topic by any connected peer and forwards it to the
// mesh BEFORE the application looks at it. Every check this package makes -
// is the proposer a validator, does the signature verify, is the height current
// - happens in a handler that runs after the relay. So an unauthenticated peer
// that can dial one node had the whole mesh spend bandwidth on its bytes.
//
// Worse than the bandwidth was the decode. The handlers do json.Unmarshal on
// attacker-controlled bytes into a Proposal, whose Block carries a transaction
// slice. Measured: a 1 MiB message of `{"block":{"txs":[{},{},...]}}` declares
// 349503 transactions - maxBlockTxs is 512 - and allocates 204 MiB of heap. It
// decodes with NO error, so nothing rejected it early. 204x amplification, from
// a peer with no key, no stake and no place in the validator set, repeatable at
// line rate.
//
// THE FIX AND WHY IT IS THIS ONE. The guard walks the JSON with a token decoder
// and refuses a payload with too many tokens. The same 1 MiB payload costs
// 1632 bytes and 20ms to walk, against 204 MiB to decode: five orders of
// magnitude cheaper, and it is a bound on STRUCTURE rather than on any
// particular field, so it covers every message type on every topic and any
// future one.
//
// What the guard deliberately does NOT do is verify signatures. It runs on
// every message from every peer before any of them is known to be worth
// anything, and a signature check there is work an attacker chooses for the
// node. The handlers keep that job, where the message has already been shown to
// be structurally sane and cheap to hold.

// maxGossipPayload is the largest gossip message this node accepts, per topic.
//
// The consensus messages that carry a lot are proposals: up to maxBlockTxs (512)
// transactions, each about 400 bytes of JSON, so around 205 KB plus a header.
// 1 MiB leaves headroom and matches gossipsub's own default, so a message this
// node would accept is one its peers can also carry.
const maxGossipPayload = 1 << 20

// maxGossipTokens bounds the number of JSON tokens in a gossip message, which
// is what bounds what decoding it can allocate.
//
// Derived rather than guessed: a token.Transaction is one object delimiter, 7
// keys, 7 values and a closing delimiter - 16 tokens. A full block at
// maxBlockTxs is 512 x 16 = 8192, plus a header and a proposal envelope, so
// under 8500. 200000 is over 20x that headroom and still refuses the measured
// attack payload (699018 tokens) by more than 3x.
const maxGossipTokens = 200000

// maxGossipDepth bounds how deeply a gossip message may nest.
//
// The token count alone does not catch nesting: 5000 nested arrays is 10002
// tokens, comfortably under maxGossipTokens, and it was accepted by the first
// version of this guard - found by its own test. Depth is a separate axis
// because skipping a deeply nested value recurses, and every consensus message
// here is shallow: a proposal is object -> block -> txs array -> tx object ->
// field, which is 5. 64 is more than a dozen times that.
const maxGossipDepth = 64

// NewGossipGuard returns the transport validator for consensus topics. It is a
// plain function rather than a method so it can be built before the Engine is,
// which is the order the node needs: the transport is constructed first and the
// engine subscribes through it.
func NewGossipGuard() func(topic string, from peer.ID, payload []byte) error {
	return func(topic string, from peer.ID, payload []byte) error {
		return GuardGossipPayload(topic, payload)
	}
}

// GuardGossipPayload applies the cheap structural checks to one gossip payload.
// It is exported so a test can drive it directly, and so a node can reuse the
// same rule if it ever gossips over a different transport.
func GuardGossipPayload(topic string, payload []byte) error {
	if len(payload) == 0 {
		return fmt.Errorf("%w: empty payload on %s", ErrInvalidMessage, topic)
	}
	if len(payload) > maxGossipPayload {
		return fmt.Errorf("%w: %s payload is %d bytes, over the %d-byte limit",
			ErrInvalidMessage, topic, len(payload), maxGossipPayload)
	}

	// Walk the tokens. This is the bound that matters: it costs almost nothing
	// and it is what stops a small message from becoming a large allocation.
	dec := json.NewDecoder(bytes.NewReader(payload))
	tokens, depth, topLevel := 0, 0, 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Malformed JSON. Refusing it here means it is not relayed, and the
			// sender's score drops - a peer sending garbage stops being a peer.
			return fmt.Errorf("%w: %s payload is not valid JSON: %v", ErrInvalidMessage, topic, err)
		}
		tokens++
		if tokens > maxGossipTokens {
			return fmt.Errorf("%w: %s payload has more than %d JSON tokens, which is more "+
				"structure than any valid consensus message has",
				ErrInvalidMessage, topic, maxGossipTokens)
		}

		// A token seen at depth zero begins a new TOP-LEVEL value. Counting them
		// here rather than asking dec.More() afterwards is the difference
		// between catching `{"a":1}{"b":2}` and not: the loop consumes to EOF,
		// so by the time it ends More() is false whatever the input was. The
		// first version of this guard used More() and accepted two values.
		if depth == 0 {
			topLevel++
			if topLevel > 1 {
				return fmt.Errorf("%w: %s payload has more than one top-level JSON value",
					ErrInvalidMessage, topic)
			}
		}

		if d, ok := tok.(json.Delim); ok {
			switch d {
			case '{', '[':
				depth++
				if depth > maxGossipDepth {
					return fmt.Errorf("%w: %s payload nests deeper than %d, which no valid "+
						"consensus message does", ErrInvalidMessage, topic, maxGossipDepth)
				}
			case '}', ']':
				depth--
			}
		}
	}
	if tokens == 0 {
		return fmt.Errorf("%w: %s payload holds no JSON", ErrInvalidMessage, topic)
	}
	// A TRUNCATED value leaves depth above zero, and Token() reports plain EOF
	// for it rather than an error - so without this check `{"block":` was
	// accepted. Found by this guard's own test.
	if depth != 0 {
		return fmt.Errorf("%w: %s payload is truncated: %d unclosed object or array",
			ErrInvalidMessage, topic, depth)
	}
	return nil
}
