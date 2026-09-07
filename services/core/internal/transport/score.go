package transport

import (
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Peer scoring: what a peer's behaviour costs it.
//
// WHY IT MATTERS HERE. A topic validator that rejects a message stops that one
// message being relayed. Scoring is what stops the PEER: gossipsub tracks a
// score per peer, suppresses gossip below one threshold, refuses to publish to
// it below a second, and ignores it entirely below a third. Without scoring
// configured, a peer that sends nothing but garbage is refused a million times
// and remains a full mesh member, so the guard's work is repeated forever
// instead of ending.
//
// WHY IT IS DANGEROUS TO CONFIGURE. Scoring can partition a network more
// reliably than any attacker: parameters tuned for a busy chain will graylist
// honest peers on a quiet one, and a graylisted peer is not merely deprioritized
// - its messages are dropped before processing, which for a validator means its
// votes stop counting. So every parameter below is either justified against this
// system's own traffic or deliberately disabled, and the disabled ones say why.
//
// The load-bearing parameter is P4, invalid message deliveries. That is the one
// the gossip guard feeds: ValidationReject increments it, ValidationIgnore does
// not, which is why the guard rejects rather than ignores.

const (
	// scoreDecayInterval is how often the counters decay. It matches pubsub's own
	// default so ScoreParameterDecay, which computes a decay factor against that
	// base, gives the half-lives it says it does.
	scoreDecayInterval = time.Second

	// scoreRetention is how long a disconnected peer's score is remembered.
	// Without it, a peer that has earned a graylist reconnects with a clean score
	// and starts again, which makes the graylist a rate limit rather than a
	// consequence.
	scoreRetention = 6 * time.Hour
)

// TopicScoreParams returns the per-topic scoring configuration.
//
// It is applied to each topic at JOIN time (Topic.SetScoreParams) rather than
// listed up front in PeerScoreParams.Topics. That is the difference between one
// mechanism and two: a topic list would be a second place to keep in sync, and
// a topic missing from it is scored by nothing at all - silently, because a
// topic with no params still works, it just stops charging anyone. Attaching at
// join means a topic cannot be subscribed to without being scored.
func TopicScoreParams() *pubsub.TopicScoreParams {
	return &pubsub.TopicScoreParams{
		// Each topic contributes at most a small positive score, so no single
		// topic can carry a peer over a threshold by itself.
		TopicWeight: 0.5,

		// P1: time in the mesh. A small, slow reward for a peer that has simply
		// been here, which is what keeps a long-lived honest peer comfortably
		// above the thresholds even during a quiet period when it delivers
		// nothing. Positive weight, as the parameter requires.
		TimeInMeshWeight:  0.0027, // ~1 point per hour at the quantum below
		TimeInMeshQuantum: time.Second,
		TimeInMeshCap:     3600, // capped at an hour's worth

		// P2: first message deliveries. Rewards a peer that hands us messages we
		// have not seen. On a quiet chain this simply pays nothing, which is why
		// it is safe here: a parameter that pays nothing costs nothing.
		FirstMessageDeliveriesWeight: 1,
		FirstMessageDeliveriesDecay:  pubsub.ScoreParameterDecay(10 * time.Minute),
		FirstMessageDeliveriesCap:    100,

		// P3: mesh message deliveries. DISABLED, deliberately.
		//
		// P3 penalizes a peer for delivering too FEW messages in the mesh, which
		// is how a busy network detects a freeloader. This network is not busy
		// and is not always producing: a validator set of two or four on an idle
		// chain delivers almost nothing for long stretches, and every honest peer
		// would accumulate the deficit-squared penalty and be graylisted - which
		// for a validator means its votes stop being processed. That is a
		// self-inflicted partition, worse than the freeloading it would catch.
		//
		// Enabling it needs a measured floor for the message rate per topic on a
		// live network. Until that number exists, zero is the honest setting.
		MeshMessageDeliveriesWeight: 0,

		// P3b: sticky penalty for being pruned while carrying a P3 penalty. With
		// P3 off there is no such penalty to be sticky about.
		MeshFailurePenaltyWeight: 0,

		// P4: invalid message deliveries. THIS is the one that matters.
		//
		// The value is the square of the counter, so the cost of sending garbage
		// grows fast: at this weight and TopicWeight, one rejected message costs
		// 50 points, five cost 1250, and six cost 1800 - past the graylist
		// threshold below. A peer flooding the guard therefore stops being a peer
		// within a handful of messages, while a single accidental rejection costs
		// 50 points that decay away in minutes.
		//
		// MEASURED, not computed. A live two-host run showed the validator being
		// called only 6 times for 60 published messages: once the score crosses
		// a threshold gossipsub stops the peer's messages reaching validation, so
		// the counter stops climbing and the score plateaus. The thresholds below
		// therefore have to be reachable within those first few rejections - an
		// earlier version put the graylist at -2500, which P4 could never reach
		// because the peer was already suppressed at -1800.
		InvalidMessageDeliveriesWeight: -100,
		InvalidMessageDeliveriesDecay:  pubsub.ScoreParameterDecay(5 * time.Minute),
	}

}

// PeerScoreParams returns the peer-wide scoring configuration and the thresholds
// that decide what a score does. Per-topic parameters are attached separately;
// see TopicScoreParams.
func PeerScoreParams() (*pubsub.PeerScoreParams, *pubsub.PeerScoreThresholds) {
	params := &pubsub.PeerScoreParams{
		// Empty: topics are scored as they are joined, so there is no list here
		// to fall out of sync with what the node actually subscribes to.
		Topics: map[string]*pubsub.TopicScoreParams{},

		// The total positive contribution of all topics together. Without a cap,
		// a peer subscribed to every topic could bank enough positive score from
		// mere presence to absorb a large amount of misbehaviour.
		TopicScoreCap: 10,

		// P5: application-specific score. Zero, because there is nothing honest
		// to say here: a peer id is a libp2p identity and a validator is an
		// ed25519 account, and this node holds no mapping between them. Giving
		// validators a positive baseline would need that mapping, and inventing
		// one from the connection would be trusting the thing being scored.
		AppSpecificScore:  func(peer.ID) float64 { return 0 },
		AppSpecificWeight: 1,

		// P6: IP colocation. Negative, as required: many peer ids behind one
		// address is the cheap way to fake a crowd.
		//
		// The threshold is 4 rather than 1 because legitimate deployments really
		// do share an address - two nodes behind one NAT was verified working
		// earlier in this project, and a strict threshold would have penalized
		// exactly that topology. Four allows a small cluster behind one address
		// and still charges a sybil farm, whose value is the SQUARE of the
		// excess: five peers costs 1, ten costs 36, twenty costs 256.
		IPColocationFactorWeight:    -50,
		IPColocationFactorThreshold: 4,

		// P7: behavioural penalties. gossipsub raises this for re-grafting before
		// the prune backoff elapses and for advertising messages via IHAVE and
		// then not delivering them on IWANT - both cheap ways to waste a peer's
		// time without ever sending an invalid message, so P4 would never see
		// them.
		BehaviourPenaltyWeight:    -10,
		BehaviourPenaltyThreshold: 6,
		BehaviourPenaltyDecay:     pubsub.ScoreParameterDecay(time.Hour),

		DecayInterval: scoreDecayInterval,
		DecayToZero:   0.01,
		RetainScore:   scoreRetention,
	}

	thresholds := &pubsub.PeerScoreThresholds{
		// All three must be <= 0 and in descending order, because a fresh honest
		// peer starts at exactly 0 and has to be above every one of them. A
		// positive threshold here would refuse every new peer, which is the
		// misconfiguration that partitions a network on the day it is deployed.
		//
		// GossipThreshold: stop gossiping to it. The mildest consequence, at the
		// cost of a couple of rejected messages.
		GossipThreshold: -500,
		// PublishThreshold: stop publishing to it.
		PublishThreshold: -1000,
		// GraylistThreshold: ignore its messages entirely. This is the one that
		// ends the work, so it has to be REACHABLE: -1500 is crossed by the sixth
		// rejected message on one topic (-1800), which a live run showed is about
		// as many as a peer gets to send before gossipsub stops delivering them
		// for validation at all. A threshold below that is decoration.
		//
		// It is still far enough from zero that an honest peer cannot arrive
		// there: five rejections is -1250, and one is -50.
		GraylistThreshold: -1500,

		// AcceptPXThreshold: only take peer-exchange suggestions from a peer that
		// has earned a positive score, so a hostile peer cannot seed us with its
		// friends. 10 is reachable by presence alone after about an hour.
		AcceptPXThreshold: 10,

		// OpportunisticGraftThreshold: a small positive value, so a node whose
		// mesh is performing badly looks for better peers rather than sitting in
		// it. Left at the documented small-positive shape.
		OpportunisticGraftThreshold: 2.5,
	}

	return params, thresholds
}
