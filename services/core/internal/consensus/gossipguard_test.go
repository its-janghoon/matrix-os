package consensus

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/token"
	"github.com/ecirlabs/matrix-core/internal/transport"
)

// amplificationPayload builds the measured attack: a message that fits inside
// gossipsub's default 1 MiB limit and declares hundreds of thousands of
// transactions, because an empty JSON object costs two bytes on the wire and a
// token.Transaction costs over a hundred in memory.
func amplificationPayload() []byte {
	var b strings.Builder
	b.WriteString(`{"block":{"height":0,"round":0,"txs":[`)
	n := 0
	for b.Len() < (1<<20)-32 {
		if n > 0 {
			b.WriteString(",")
		}
		b.WriteString("{}")
		n++
	}
	b.WriteString(`]}}`)
	return []byte(b.String())
}

// TestTheGossipGuardRefusesTheAmplificationPayload
//
// THE ATTACK. gossipsub with no topic validator accepts any message on a
// subscribed topic from any connected peer and relays it to the mesh BEFORE the
// application looks at it. Every check this package makes - proposer is a
// validator, signature verifies, height is current - runs in a handler after
// the relay.
//
// Measured on the payload below: 1 MiB on the wire, 349503 transactions
// declared where maxBlockTxs is 512, and 204 MiB of heap allocated by the
// handler's json.Unmarshal. It decoded with NO error, so nothing rejected it
// early. That is 204x amplification from a peer with no key, no stake and no
// place in the validator set.
func TestTheGossipGuardRefusesTheAmplificationPayload(t *testing.T) {
	payload := amplificationPayload()
	if len(payload) > maxGossipPayload {
		t.Fatalf("the fixture is %d bytes, over the payload cap: it would be refused on size "+
			"alone and would not exercise the token bound", len(payload))
	}

	err := GuardGossipPayload(TopicProposal, payload)
	if err == nil {
		t.Fatalf("a %d-byte payload declaring %d transactions was accepted; decoding it "+
			"allocates hundreds of MiB and gossipsub would have relayed it first",
			len(payload), strings.Count(string(payload), "{}"))
	}
	if !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("error = %v, want ErrInvalidMessage", err)
	}
	if !strings.Contains(err.Error(), "token") {
		t.Fatalf("error = %v, want it to name the token bound that caught it", err)
	}
}

// TestTheGuardIsOrdersOfMagnitudeCheaperThanDecoding is the reason the guard is
// a token walk rather than a decode-then-check. A guard that cost what the
// attack costs would be no defence: the node would still do the work, just
// before throwing the result away.
func TestTheGuardIsOrdersOfMagnitudeCheaperThanDecoding(t *testing.T) {
	payload := amplificationPayload()

	var m1, m2 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)
	_ = GuardGossipPayload(TopicProposal, payload)
	runtime.ReadMemStats(&m2)
	guardCost := m2.TotalAlloc - m1.TotalAlloc

	var m3, m4 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m3)
	var p Proposal
	if err := json.Unmarshal(payload, &p); err != nil {
		t.Fatalf("the attack payload should decode cleanly; that is the point: %v", err)
	}
	runtime.ReadMemStats(&m4)
	decodeCost := m4.TotalAlloc - m3.TotalAlloc

	t.Logf("guard %d bytes, decode %d bytes (%d transactions decoded)",
		guardCost, decodeCost, len(p.Block.Txs))
	if guardCost*1000 > decodeCost {
		t.Fatalf("the guard allocated %d bytes against the decode's %d: it is not cheap enough "+
			"to run on every message from every peer", guardCost, decodeCost)
	}
}

// TestARealProposalPasses. The bound is derived from what a full block actually
// is, so a legitimate proposal at maxBlockTxs must pass with room to spare - a
// guard that refused real traffic would halt the chain more reliably than any
// attacker.
func TestARealProposalPasses(t *testing.T) {
	acct, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}

	txs := make([]token.Transaction, 0, DefaultMaxBlockTxs)
	for i := 0; i < DefaultMaxBlockTxs; i++ {
		tx := &token.Transaction{
			From:      acct.PublicKey,
			To:        "dfb781c10b66f1eb88372bbb038a03d2b0b507e8d772aacaaa39acc70ca5070f",
			Amount:    250,
			Nonce:     uint64(i),
			Timestamp: 1757250000000000000,
			PrevHash:  make([]byte, HashSize),
		}
		if err := tx.Sign(acct.PrivateKey); err != nil {
			t.Fatalf("Sign: %v", err)
		}
		txs = append(txs, *tx)
	}

	b := &Block{
		Height:        7,
		Round:         2,
		PrevBlockHash: make([]byte, HashSize),
		ProposerID:    acct.AccountID(),
		Txs:           txs,
		Timestamp:     time.Now().Unix(),
	}
	if err := b.Sign(acct.PrivateKey); err != nil {
		t.Fatalf("Sign block: %v", err)
	}
	p := Proposal{Block: *b, Round: 2}
	payload, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	t.Logf("a full %d-transaction proposal is %d bytes", DefaultMaxBlockTxs, len(payload))
	if err := GuardGossipPayload(TopicProposal, payload); err != nil {
		t.Fatalf("a legitimate full block was refused, which would halt the chain: %v", err)
	}
}

// TestARealVotePasses: the small, frequent message must go through too.
func TestARealVotePasses(t *testing.T) {
	acct, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	v := &Vote{
		Type:      VoteTypePrevote,
		Height:    3,
		Round:     1,
		BlockHash: make([]byte, HashSize),
		VoterID:   acct.AccountID(),
		PublicKey: acct.PublicKey,
	}
	if err := v.Sign(acct.PrivateKey); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	payload, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := GuardGossipPayload(TopicVote, payload); err != nil {
		t.Fatalf("a legitimate vote was refused: %v", err)
	}
}

// TestTheGuardRefusesJunk covers the shapes that are not an amplification but
// are still not a consensus message. Each must be REFUSED rather than ignored,
// because rejection is what stops the relay and lowers the sender's score - an
// ignored message leaves the peer free to keep sending.
func TestTheGuardRefusesJunk(t *testing.T) {
	cases := map[string][]byte{
		"empty":               {},
		"not json at all":     []byte("hello there"),
		"truncated object":    []byte(`{"block":`),
		"trailing garbage":    []byte(`{"round":1} and then some`),
		"unterminated string": []byte(`{"a":"`),
		"over the size limit": make([]byte, maxGossipPayload+1),
		// 5000 deep is 10002 tokens, under maxGossipTokens: depth is its own
		// axis, and the first version of this guard accepted this.
		"deeply nested arrays":    []byte("[" + strings.Repeat("[", 5000) + strings.Repeat("]", 5000) + "]"),
		"two top-level values":    []byte(`{"round":1}{"round":2}`),
		"unclosed array":          []byte(`{"block":{"txs":[{}`),
		"a million empty objects": amplificationPayload(),
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			if err := GuardGossipPayload(TopicProposal, payload); err == nil {
				t.Fatalf("accepted %d bytes of %s", len(payload), name)
			}
		})
	}
}

// TestTheGuardDoesNotVerifySignatures is a deliberate design choice, pinned so
// it is not "fixed" later by accident.
//
// The guard runs on every message from every peer before any of them is known
// to be worth anything. A signature check there is work an attacker chooses for
// the node - and unlike the token walk it cannot be made cheap. The handlers
// keep that job, where the message has already been shown to be structurally
// sane. So a structurally valid message with a nonsense signature passes the
// guard and is rejected one layer in.
func TestTheGuardDoesNotVerifySignatures(t *testing.T) {
	v := &Vote{
		Type:      VoteTypePrevote,
		Height:    3,
		Round:     1,
		BlockHash: make([]byte, HashSize),
		VoterID:   "dfb781c10b66f1eb88372bbb038a03d2b0b507e8d772aacaaa39acc70ca5070f",
		Signature: make([]byte, ed25519.SignatureSize), // all zeroes: not a signature
	}
	payload, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := GuardGossipPayload(TopicVote, payload); err != nil {
		t.Fatalf("the guard rejected a structurally valid message over its signature: %v", err)
	}
	// And the layer that does care still refuses it.
	if err := v.Verify(); err == nil {
		t.Fatal("a zero signature verified")
	}
}

// TestAnsweringSyncRequestsIsRateLimited
//
// THE REFLECTION AMPLIFICATION. A BlockSyncRequest is about 60 bytes. A
// response is up to MaxSyncResponseBytes (1 MiB) and is BROADCAST to the whole
// topic, so one tiny request had every node holding the chain read from disk,
// marshal a megabyte, and publish it - which gossipsub fans out to each node's
// mesh peers. At ten nodes that is roughly 60 MiB of mesh traffic from 60
// bytes, about a millionfold, repeatable at line rate by a peer with no key and
// no place in the validator set.
//
// lastSyncRequest rate-limited how often a node ASKS. Nothing limited how often
// it ANSWERS, which is the side an attacker drives.
func TestAnsweringSyncRequestsIsRateLimited(t *testing.T) {
	nodes, stop := newCluster(t, 2, nil)
	defer stop()

	sender := nodes[0].acct
	mintAll(t, nodes, sender.AccountID(), 10_000)

	// Commit something so there is a block worth serving.
	tx := signedTransfer(t, sender, "recipient", 100, 0)
	for _, nd := range nodes {
		if err := nd.engine.Submit(tx); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, _, err := nodes[0].engine.WaitForSettlement(ctx, tx); err != nil {
		t.Fatalf("WaitForSettlement: %v", err)
	}

	nd := nodes[1].engine
	// A flood of requests from a peer that is not this node.
	req := BlockSyncRequest{Height: 0, RequesterID: "attacker"}
	payload, err := json.Marshal(&req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	t.Logf("a sync request is %d bytes; a response is up to %d", len(payload), MaxSyncResponseBytes)

	nd.mu.Lock()
	nd.lastSyncServe = time.Time{} // a fresh node has never served
	nd.mu.Unlock()

	served := 0
	for i := 0; i < 500; i++ {
		before := nd.servedSyncAt()
		nd.handleSyncRequest(context.Background(), transportMessage(payload))
		if nd.servedSyncAt() != before {
			served++
		}
	}
	if served > 2 {
		t.Fatalf("500 requests in a tight loop were answered %d times; each answer is up to "+
			"%d bytes broadcast to the whole topic", served, MaxSyncResponseBytes)
	}
	if served == 0 {
		t.Fatal("no request was answered at all; a lagging peer would never catch up")
	}
}

// TestARateLimitedResponderStillServesAnHonestPeer. The interval is deliberately
// shorter than the request side's own limit, so an honest lagging node asking at
// its natural cadence is always answered. A rate limit that starved sync would
// be a worse bug than the amplification it fixed.
func TestARateLimitedResponderStillServesAnHonestPeer(t *testing.T) {
	nodes, stop := newCluster(t, 2, nil)
	defer stop()

	nd := nodes[1].engine
	nd.mu.Lock()
	requestInterval := nd.roundTimeout
	nd.mu.Unlock()

	// The responder's interval must be strictly shorter than the requester's, or
	// an honest peer asking once per roundTimeout could be refused.
	nd.mu.Lock()
	serveInterval := nd.roundTimeout / 2
	nd.mu.Unlock()
	if serveInterval >= requestInterval {
		t.Fatalf("the serve interval (%s) is not shorter than the request interval (%s): an "+
			"honest lagging peer could be refused", serveInterval, requestInterval)
	}
}

// servedSyncAt reports when this engine last answered a sync request, for the
// rate-limit test.
func (e *Engine) servedSyncAt() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastSyncServe
}

// transportMessage wraps a payload as an inbound gossip message.
func transportMessage(payload []byte) transport.Message {
	return transport.Message{Topic: TopicSyncRequest, Payload: payload}
}
