package consensus

import (
	"encoding/binary"
	"encoding/json"
	"fmt"

	"github.com/ecirlabs/matrix-core/internal/kv"
)

// What a seller has actually been paid, according to the chain.
//
// WHY THIS AND NOT A RATING. A marketplace is asked for uptime, latency and a
// star score, and a seller can type any number into all three. The directory
// carries none of them for that reason. What cannot be typed in is a settled
// transfer: an inference job is paid by a consensus transfer from buyer to
// provider, committed in a block every node holds, and a payment that did not
// happen leaves no record to find.
//
// WHAT IT DOES NOT PROVE, stated here because the number will be read as more
// than it is. A settlement is an ORDINARY transfer - the chain does not mark it
// as an inference payment, because nothing in the protocol needs it to - so this
// counts every payment an account received, not only the ones it earned. A
// seller can pay itself from a second account it controls. What that costs is
// the protocol fee on each transfer plus the capital to move, which is why
// Payers is the number to look at rather than Received: inflating the total is
// one self-transfer, and inflating the payer count means funding a fresh account
// for each one.
//
// So this is history, not a rating, and it should be presented as history.
//
// WHY IT IS PERSISTED rather than recomputed. Whether a committed transfer
// actually APPLIED depended on the sender's balance at that height, and a node
// that restarts no longer knows: the ledger holds today's balances, not the
// sequence that produced them. Replaying the whole chain to recover it would
// duplicate the work the persisted ledger exists to avoid. So the tally is
// written as blocks apply and carries the height it began at - anything before
// that is not counted and is not claimed to be.

const (
	earningsKeyPrefix  = "consensus/earnings/acct/"
	earningsPairPrefix = "consensus/earnings/pair/"
	earningsFromKey    = "consensus/earnings/indexed-from"
	earningsHeightKey  = "consensus/earnings/indexed-height"
)

// Earnings is what one account has been paid since indexing began.
type Earnings struct {
	// Received is the total credited, net of the protocol fee - what the account
	// actually got, not what the buyer was charged.
	Received uint64 `json:"received"`
	// Payments is how many applied transfers made it up.
	Payments uint64 `json:"payments"`
	// Payers is how many DISTINCT accounts have paid. The number that costs
	// something to inflate.
	Payers uint64 `json:"payers"`
	// FirstHeight and LastHeight bracket the activity, so a reader can tell a
	// seller that has been working for months from one paid twice yesterday.
	FirstHeight uint64 `json:"first_height"`
	LastHeight  uint64 `json:"last_height"`
}

// EarningsStore holds the per-account payment tally.
type EarningsStore struct {
	store *kv.Store
}

// NewEarningsStore wraps a kv store.
func NewEarningsStore(store *kv.Store) *EarningsStore {
	return &EarningsStore{store: store}
}

// payment is one applied transfer into an ordinary account.
type payment struct {
	payer  string
	payee  string
	amount uint64
}

func earningsKey(account string) []byte { return []byte(earningsKeyPrefix + account) }

// pairKey marks that one payer has paid one payee at least once. The payee comes
// first so the pairs of one seller sit together in key order, which is what a
// prefix read would need.
func pairKey(payee, payer string) []byte {
	return []byte(earningsPairPrefix + payee + "\x00" + payer)
}

// Get returns what an account has been paid. An account that has never been paid
// reports a zero record rather than an error: "nobody has paid this seller" is an
// answer, and the commonest one on a young network.
func (s *EarningsStore) Get(account string) (Earnings, error) {
	if s == nil || s.store == nil {
		return Earnings{}, nil
	}
	body, err := s.store.Get(earningsKey(account))
	if err != nil {
		return Earnings{}, fmt.Errorf("consensus: read earnings for %s: %w", account, err)
	}
	if body == nil {
		return Earnings{}, nil
	}
	var e Earnings
	if err := json.Unmarshal(body, &e); err != nil {
		return Earnings{}, fmt.Errorf("consensus: decode earnings for %s: %w", account, err)
	}
	return e, nil
}

// IndexedFrom returns the first height this tally covers, and whether indexing
// has begun at all.
//
// It exists so a display can say "paid 41 times since height 900" rather than
// implying it counted from genesis. A node that upgraded into this code counts
// from the upgrade, and saying so is the difference between an honest number and
// a misleading one.
func (s *EarningsStore) IndexedFrom() (uint64, bool, error) {
	if s == nil || s.store == nil {
		return 0, false, nil
	}
	body, err := s.store.Get([]byte(earningsFromKey))
	if err != nil {
		return 0, false, fmt.Errorf("consensus: read earnings start height: %w", err)
	}
	if len(body) != 8 {
		return 0, false, nil
	}
	return binary.BigEndian.Uint64(body), true, nil
}

// record applies one block's payments in a single batch.
//
// One batch, so a block's tallies and the height marking them done land together
// or not at all. A crash between two separate writes would leave a height saying
// a block was counted when half of it was, and the number would be wrong forever
// with nothing to notice it.
//
// Deliberately NOT re-derivable: a block whose batch never committed is skipped
// on restart rather than replayed, because whether its transfers applied is no
// longer knowable. That undercounts, which is the safe direction for a figure a
// buyer reads as a seller's track record.
func (s *EarningsStore) record(height uint64, payments []payment) error {
	if s == nil || s.store == nil || len(payments) == 0 {
		return nil
	}

	// Fold the block's payments per payee first, so one payee paid twice in a
	// block costs one read rather than two.
	totals := make(map[string]*Earnings, len(payments))
	newPairs := make(map[string][]byte)
	for _, p := range payments {
		acc, ok := totals[p.payee]
		if !ok {
			existing, err := s.Get(p.payee)
			if err != nil {
				return err
			}
			acc = &existing
			totals[p.payee] = acc
		}
		if acc.Payments == 0 {
			acc.FirstHeight = height
		}
		acc.Received += p.amount
		acc.Payments++
		acc.LastHeight = height

		key := pairKey(p.payee, p.payer)
		if _, staged := newPairs[string(key)]; staged {
			continue
		}
		seen, err := s.store.Get(key)
		if err != nil {
			return fmt.Errorf("consensus: read payer pair: %w", err)
		}
		if seen == nil {
			acc.Payers++
			newPairs[string(key)] = key
		}
	}

	batch := s.store.NewBatch()
	defer func() { _ = batch.Close() }()

	for account, acc := range totals {
		body, err := json.Marshal(acc)
		if err != nil {
			return fmt.Errorf("consensus: encode earnings for %s: %w", account, err)
		}
		if err := batch.Set(earningsKey(account), body, nil); err != nil {
			return err
		}
	}
	for _, key := range newPairs {
		if err := batch.Set(key, []byte{1}, nil); err != nil {
			return err
		}
	}

	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], height)
	if err := batch.Set([]byte(earningsHeightKey), buf[:], nil); err != nil {
		return err
	}
	// Written once, on the first block that records anything, and never moved
	// after. It is what lets the display bound its own claim.
	if _, started, err := s.IndexedFrom(); err != nil {
		return err
	} else if !started {
		if err := batch.Set([]byte(earningsFromKey), buf[:], nil); err != nil {
			return err
		}
	}
	return batch.Commit(nil)
}

// Earnings returns what an account has been paid, and the height the tally
// starts from. It is on the Engine so a caller holding one does not need to know
// which store it lives in.
func (e *Engine) Earnings(account string) (Earnings, uint64, error) {
	got, err := e.earnings.Get(account)
	if err != nil {
		return Earnings{}, 0, err
	}
	from, _, err := e.earnings.IndexedFrom()
	if err != nil {
		return Earnings{}, 0, err
	}
	return got, from, nil
}
