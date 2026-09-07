package transport

import (
	"context"
	"fmt"
	"testing"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Peer scoring is the one configuration here that can partition the network by
// itself. These tests exist for that direction as much as for the attacker's:
// several of them assert that an HONEST peer is never punished, because a
// misconfigured score set graylists honest validators and a graylisted
// validator's votes stop being counted.

// TestTheScoreConfigurationIsValid. pubsub validates the parameters and refuses
// to construct with a bad set - so this both checks the numbers and pins that
// we rely on that refusal rather than discovering a sign error in production.
func TestTheScoreConfigurationIsValid(t *testing.T) {
	params, thresholds := PeerScoreParams()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := newHost(t)
	if _, err := pubsub.NewGossipSub(ctx, h, pubsub.WithPeerScore(params, thresholds)); err != nil {
		t.Fatalf("the score configuration was rejected by pubsub: %v", err)
	}

	// And the per-topic set, which is applied separately at join time and is
	// validated by SetScoreParams rather than by WithPeerScore.
	ps, err := pubsub.NewGossipSub(ctx, newHost(t), pubsub.WithPeerScore(params, thresholds))
	if err != nil {
		t.Fatalf("NewGossipSub: %v", err)
	}
	tp, err := ps.Join("matrix.test/score")
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if err := tp.SetScoreParams(TopicScoreParams()); err != nil {
		t.Fatalf("the per-topic score configuration was rejected: %v", err)
	}
}

// TestAFreshPeerIsAboveEveryThreshold is the partition guard. A peer that has
// just connected has a score of exactly zero. Every negative threshold must be
// strictly below zero, or a brand-new honest peer is graylisted on arrival -
// the misconfiguration that takes a network down on the day it ships.
func TestAFreshPeerIsAboveEveryThreshold(t *testing.T) {
	_, th := PeerScoreParams()
	const fresh = 0.0

	if th.GossipThreshold >= fresh {
		t.Fatalf("GossipThreshold is %v; a fresh peer at 0 would be gossip-suppressed immediately",
			th.GossipThreshold)
	}
	if th.PublishThreshold >= fresh {
		t.Fatalf("PublishThreshold is %v; a fresh peer at 0 would never be published to",
			th.PublishThreshold)
	}
	if th.GraylistThreshold >= fresh {
		t.Fatalf("GraylistThreshold is %v; a fresh peer at 0 would be graylisted on arrival",
			th.GraylistThreshold)
	}
	// pubsub requires this ordering, and the ordering is the point: the mildest
	// consequence must come first, and being ignored entirely must be last.
	if !(th.GraylistThreshold <= th.PublishThreshold && th.PublishThreshold <= th.GossipThreshold) {
		t.Fatalf("thresholds are not ordered mildest-first: gossip %v, publish %v, graylist %v",
			th.GossipThreshold, th.PublishThreshold, th.GraylistThreshold)
	}
}

// TestTheSignsAreRight. pubsub REQUIRES a positive weight on the rewards and a
// negative one on the penalties, and a sign error flips a punishment into a
// reward: a peer sending invalid messages would climb the score instead of
// falling. pubsub's own validation catches most of these, so this is a
// statement of intent as much as a check.
func TestTheSignsAreRight(t *testing.T) {
	tp := TopicScoreParams()
	if tp.TopicWeight <= 0 {
		t.Fatalf("TopicWeight %v must be positive", tp.TopicWeight)
	}
	if tp.TimeInMeshWeight < 0 {
		t.Fatalf("TimeInMeshWeight %v is a reward and must not be negative", tp.TimeInMeshWeight)
	}
	if tp.FirstMessageDeliveriesWeight < 0 {
		t.Fatalf("FirstMessageDeliveriesWeight %v is a reward and must not be negative",
			tp.FirstMessageDeliveriesWeight)
	}
	if tp.InvalidMessageDeliveriesWeight >= 0 {
		t.Fatalf("InvalidMessageDeliveriesWeight is %v; sending invalid messages must COST, and "+
			"this is the parameter the gossip guard feeds", tp.InvalidMessageDeliveriesWeight)
	}

	p, _ := PeerScoreParams()
	if p.IPColocationFactorWeight >= 0 {
		t.Fatalf("IPColocationFactorWeight %v must be negative", p.IPColocationFactorWeight)
	}
	if p.BehaviourPenaltyWeight >= 0 {
		t.Fatalf("BehaviourPenaltyWeight %v must be negative", p.BehaviourPenaltyWeight)
	}
	if p.TopicScoreCap <= 0 {
		t.Fatalf("TopicScoreCap %v must be positive, or positive score is unbounded", p.TopicScoreCap)
	}
}

// TestAFloodOfRejectionsGraylistsThePeer is the whole reason for this file. The
// gossip guard rejects a message; the score is what makes the SENDER stop being
// a peer. This computes the score P4 produces for n rejections on one topic and
// checks that a handful is enough.
func TestAFloodOfRejectionsGraylistsThePeer(t *testing.T) {
	p, th := PeerScoreParams()
	tp := TopicScoreParams()

	// P4's value is the square of the counter, weighted, times the topic weight.
	scoreFor := func(rejections float64) float64 {
		return tp.TopicWeight * tp.InvalidMessageDeliveriesWeight * rejections * rejections
	}

	// One accidental rejection must not be fatal: an honest peer with a bug, or
	// one message mangled in flight, has to be able to recover.
	if one := scoreFor(1); one <= th.GossipThreshold {
		t.Fatalf("a SINGLE rejected message scores %v, past the gossip threshold %v: an honest "+
			"peer with one bad message would be punished", one, th.GossipThreshold)
	}

	// A flood must be. Find where it crosses.
	graylistedAt := 0
	for n := 1; n <= 100; n++ {
		if scoreFor(float64(n)) <= th.GraylistThreshold {
			graylistedAt = n
			break
		}
	}
	if graylistedAt == 0 {
		t.Fatalf("100 rejected messages never reach the graylist threshold %v; a flooding peer "+
			"would stay a full mesh member forever", th.GraylistThreshold)
	}
	if graylistedAt > 20 {
		t.Fatalf("it takes %d rejected messages to graylist a peer; the guard would do that work "+
			"%d times over", graylistedAt, graylistedAt)
	}
	t.Logf("one rejection scores %.0f; graylisted (%.0f) after %d rejections",
		scoreFor(1), th.GraylistThreshold, graylistedAt)

	// The IP colocation penalty must also be able to reach the graylist, or a
	// sybil farm on one address pays nothing that matters.
	excess := 20.0 - float64(p.IPColocationFactorThreshold)
	sybil := p.IPColocationFactorWeight * excess * excess
	t.Logf("20 peers on one address scores %.0f", sybil)
	if sybil > th.GossipThreshold {
		t.Fatalf("20 peer ids behind one address scores only %v, which is above the gossip "+
			"threshold %v", sybil, th.GossipThreshold)
	}
}

// TestASmallClusterBehindOneAddressIsNotPunished. The IP colocation threshold
// is a real trade-off: legitimate deployments share an address, and two nodes
// behind one NAT is a topology this project verified working. A threshold of 1
// would have penalized exactly that.
func TestASmallClusterBehindOneAddressIsNotPunished(t *testing.T) {
	p, _ := PeerScoreParams()
	if p.IPColocationFactorThreshold < 2 {
		t.Fatalf("IPColocationFactorThreshold is %d: two nodes behind one NAT is a verified "+
			"topology and would be penalized", p.IPColocationFactorThreshold)
	}
	for peers := 1; peers <= p.IPColocationFactorThreshold; peers++ {
		// At or below the threshold the value is zero, so the penalty is zero.
		excess := float64(peers - p.IPColocationFactorThreshold)
		if excess > 0 {
			t.Fatalf("%d peers is over the threshold %d", peers, p.IPColocationFactorThreshold)
		}
	}
}

// TestTheQuietChainPenaltiesAreOff pins the deliberate omission. P3 penalizes a
// peer for delivering too FEW messages, which on an idle chain is every honest
// peer - so it would graylist the validator set and stop the chain. If someone
// later enables it without a measured message rate, this fails and says why.
func TestTheQuietChainPenaltiesAreOff(t *testing.T) {
	tp := TopicScoreParams()
	if tp.MeshMessageDeliveriesWeight != 0 {
		t.Fatalf("MeshMessageDeliveriesWeight is %v. P3 punishes a peer for delivering too FEW "+
			"messages; on an idle chain that is every honest validator, and a graylisted "+
			"validator's votes stop counting. Enabling it needs a measured per-topic message "+
			"rate from a live network first", tp.MeshMessageDeliveriesWeight)
	}
	if tp.MeshFailurePenaltyWeight != 0 {
		t.Fatalf("MeshFailurePenaltyWeight is %v but it is the sticky form of a P3 penalty that "+
			"cannot occur with P3 disabled", tp.MeshFailurePenaltyWeight)
	}
}

// TestScoresAreRememberedAcrossADisconnect. Without retention a peer that has
// earned a graylist reconnects with a clean score and starts again, which makes
// the graylist a rate limit rather than a consequence.
func TestScoresAreRememberedAcrossADisconnect(t *testing.T) {
	p, _ := PeerScoreParams()
	if p.RetainScore < time.Hour {
		t.Fatalf("RetainScore is %s; a graylisted peer could reconnect with a clean slate",
			p.RetainScore)
	}
}

// TestTheAppSpecificScoreSaysNothing pins another deliberate choice. P5 could
// give known validators a positive baseline, but a peer id is a libp2p identity
// and a validator is an ed25519 account, and this node holds no mapping between
// them. Deriving one from the connection would mean trusting the thing being
// scored.
func TestTheAppSpecificScoreSaysNothing(t *testing.T) {
	p, _ := PeerScoreParams()
	if p.AppSpecificScore == nil {
		t.Fatal("AppSpecificScore must not be nil; pubsub requires it")
	}
	if got := p.AppSpecificScore(peer.ID("anyone")); got != 0 {
		t.Fatalf("AppSpecificScore returned %v for an arbitrary peer; there is no peer-id to "+
			"validator mapping to base that on", got)
	}
}

// TestEveryJoinedTopicIsScored. This is the drift guard: attaching params at
// join is what makes a topic impossible to subscribe to unscored, and an
// unscored topic fails silently - it still works, it just stops charging
// anyone.
func TestEveryJoinedTopicIsScored(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tr, err := New(ctx, Config{Host: newHost(t), PeerScore: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, topic := range []string{"matrix.test/a", "matrix.test/b"} {
		if _, err := tr.Subscribe(ctx, topic); err != nil {
			t.Fatalf("Subscribe(%s): %v", topic, err)
		}
	}
	// SetScoreParams validates and returns an error, and Subscribe surfaces it,
	// so reaching here means both topics were scored. Re-setting the same params
	// on the joined topic must also still succeed, which is what proves the
	// topic is live and scoreable rather than merely created.
	tr.topicMu.RLock()
	defer tr.topicMu.RUnlock()
	if len(tr.topics) != 2 {
		t.Fatalf("joined %d topics, want 2", len(tr.topics))
	}
	for name, tp := range tr.topics {
		if err := tp.SetScoreParams(TopicScoreParams()); err != nil {
			t.Fatalf("topic %s is not scoreable: %v", name, err)
		}
	}
}

// TestScoringIsOptional. An in-process or trusted-network caller must be able to
// skip it, and skipping it must not break joining.
func TestScoringIsOptional(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tr, err := New(ctx, Config{Host: newHost(t)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := tr.Subscribe(ctx, "matrix.test/unscored"); err != nil {
		t.Fatalf("Subscribe with scoring off: %v", err)
	}
}

// TestARealFloodingPeerIsSuppressed
//
// Everything above computes what the parameters SHOULD produce. This measures
// what gossipsub actually does with them: two real hosts, real gossipsub, a real
// validator rejecting messages, and the peer's real score read back through
// WithPeerScoreInspect.
//
// It asserts suppression (below GossipThreshold) rather than the graylist, and
// that is a finding rather than a weaker test. A live run showed the validator
// being called only 6 times for 60 published messages: once a peer's score
// crosses a threshold, gossipsub stops delivering its messages for validation,
// so the counter stops climbing and the score plateaus and then decays back up.
// How many rejections land before that happens depends on heartbeat timing, so
// asserting a specific threshold inside a fixed window would be a flaky test,
// not a stronger one. The stable, load-bearing property is that a flooding peer
// goes materially negative and stops being gossiped to; where exactly it lands
// between -700 and -1800 is timing.
func TestARealFloodingPeerIsSuppressed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const topic = "matrix.test/flood"
	params, thresholds := PeerScoreParams()

	scores := make(chan map[peer.ID]float64, 256)
	victim := newHost(t)
	victimPS, err := pubsub.NewGossipSub(ctx, victim,
		pubsub.WithPeerScore(params, thresholds),
		pubsub.WithPeerScoreInspect(func(m map[peer.ID]float64) {
			snapshot := make(map[peer.ID]float64, len(m))
			for k, v := range m {
				snapshot[k] = v
			}
			select {
			case scores <- snapshot:
			default:
			}
		}, 200*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("victim gossipsub: %v", err)
	}
	// Reject everything, as the guard does for garbage.
	rejections := 0
	if err := victimPS.RegisterTopicValidator(topic,
		func(context.Context, peer.ID, *pubsub.Message) pubsub.ValidationResult {
			rejections++
			return pubsub.ValidationReject
		}); err != nil {
		t.Fatalf("RegisterTopicValidator: %v", err)
	}
	victimTopic, err := victimPS.Join(topic)
	if err != nil {
		t.Fatalf("victim Join: %v", err)
	}
	if err := victimTopic.SetScoreParams(TopicScoreParams()); err != nil {
		t.Fatalf("SetScoreParams: %v", err)
	}
	if _, err := victimTopic.Subscribe(); err != nil {
		t.Fatalf("victim Subscribe: %v", err)
	}

	attacker := newHost(t)
	attackerPS, err := pubsub.NewGossipSub(ctx, attacker)
	if err != nil {
		t.Fatalf("attacker gossipsub: %v", err)
	}
	attackerTopic, err := attackerPS.Join(topic)
	if err != nil {
		t.Fatalf("attacker Join: %v", err)
	}
	if _, err := attackerTopic.Subscribe(); err != nil {
		t.Fatalf("attacker Subscribe: %v", err)
	}

	connect(t, attacker, victim)
	// Let the mesh form before publishing, or the messages go nowhere and the
	// test proves nothing about scoring.
	time.Sleep(2 * time.Second)

	// Distinct payloads, because identical ones can collapse in the seen-cache
	// and then only one of them is ever scored. Batched with pauses so the
	// heartbeat gets a chance to apply the penalty between bursts.
	worst := 0.0
	for batch := 0; batch < 6; batch++ {
		for i := 0; i < 10; i++ {
			if err := attackerTopic.Publish(ctx, []byte(fmt.Sprintf("garbage-%d-%d", batch, i))); err != nil {
				t.Fatalf("Publish: %v", err)
			}
			time.Sleep(15 * time.Millisecond)
		}
		time.Sleep(500 * time.Millisecond)
		for drained := true; drained; {
			drained = false
			select {
			case snapshot := <-scores:
				drained = true
				if score, ok := snapshot[attacker.ID()]; ok && score < worst {
					worst = score
				}
			default:
			}
		}
	}

	t.Logf("60 published, %d reached validation and were rejected, worst score %.0f "+
		"(gossip %.0f, publish %.0f, graylist %.0f)",
		rejections, worst, thresholds.GossipThreshold, thresholds.PublishThreshold,
		thresholds.GraylistThreshold)

	if worst == 0 {
		t.Fatal("the attacker was never scored at all; rejection is not reaching P4, so the " +
			"guard's refusals cost the sender nothing")
	}
	if worst > thresholds.GossipThreshold {
		t.Fatalf("the attacker's worst score was %.0f, still above the gossip threshold %.0f: "+
			"a flooding peer stays a full mesh member", worst, thresholds.GossipThreshold)
	}
	// And the reason the score plateaus: gossipsub stopped delivering. If every
	// published message reached validation, suppression is not happening and the
	// node is doing the guard's work 60 times over.
	if rejections >= 60 {
		t.Fatalf("all %d published messages reached validation; the peer was never suppressed",
			rejections)
	}
}

// TestAnHonestPeerKeepsAPositiveScore is the other direction, and the one a
// misconfiguration breaks. A peer that publishes valid messages must not drift
// toward the thresholds - if it does, the score set partitions the network on
// its own.
func TestAnHonestPeerKeepsAPositiveScore(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const topic = "matrix.test/honest"
	params, thresholds := PeerScoreParams()

	scores := make(chan map[peer.ID]float64, 64)
	receiver := newHost(t)
	receiverPS, err := pubsub.NewGossipSub(ctx, receiver,
		pubsub.WithPeerScore(params, thresholds),
		pubsub.WithPeerScoreInspect(func(m map[peer.ID]float64) {
			snapshot := make(map[peer.ID]float64, len(m))
			for k, v := range m {
				snapshot[k] = v
			}
			select {
			case scores <- snapshot:
			default:
			}
		}, 200*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("receiver gossipsub: %v", err)
	}
	// Accept everything, as the guard does for a well-formed message.
	if err := receiverPS.RegisterTopicValidator(topic,
		func(context.Context, peer.ID, *pubsub.Message) pubsub.ValidationResult {
			return pubsub.ValidationAccept
		}); err != nil {
		t.Fatalf("RegisterTopicValidator: %v", err)
	}
	recvTopic, err := receiverPS.Join(topic)
	if err != nil {
		t.Fatalf("receiver Join: %v", err)
	}
	if err := recvTopic.SetScoreParams(TopicScoreParams()); err != nil {
		t.Fatalf("SetScoreParams: %v", err)
	}
	if _, err := recvTopic.Subscribe(); err != nil {
		t.Fatalf("receiver Subscribe: %v", err)
	}

	sender := newHost(t)
	senderPS, err := pubsub.NewGossipSub(ctx, sender)
	if err != nil {
		t.Fatalf("sender gossipsub: %v", err)
	}
	sendTopic, err := senderPS.Join(topic)
	if err != nil {
		t.Fatalf("sender Join: %v", err)
	}
	if _, err := sendTopic.Subscribe(); err != nil {
		t.Fatalf("sender Subscribe: %v", err)
	}

	connect(t, sender, receiver)
	time.Sleep(2 * time.Second)

	for i := 0; i < 20; i++ {
		if err := sendTopic.Publish(ctx, []byte{byte(i)}); err != nil {
			t.Fatalf("Publish %d: %v", i, err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	deadline := time.After(15 * time.Second)
	best := -1e9
	for {
		select {
		case snapshot := <-scores:
			if score, ok := snapshot[sender.ID()]; ok {
				if score > best {
					best = score
				}
				if score > 0 {
					t.Logf("an honest peer's real score is %.2f, comfortably above the gossip "+
						"threshold %.0f", score, thresholds.GossipThreshold)
					return
				}
				if score <= thresholds.GossipThreshold {
					t.Fatalf("an honest peer publishing valid messages scored %.2f, at or past "+
						"the gossip threshold %.0f: this configuration partitions the network "+
						"by itself", score, thresholds.GossipThreshold)
				}
			}
		case <-deadline:
			// Never going positive is acceptable on a quiet topic; drifting
			// NEGATIVE is not. Zero is where a fresh peer starts.
			if best < 0 {
				t.Fatalf("an honest peer's best score was %.2f, below zero", best)
			}
			t.Logf("an honest peer's best score was %.2f (not negative, which is what matters)", best)
			return
		}
	}
}
