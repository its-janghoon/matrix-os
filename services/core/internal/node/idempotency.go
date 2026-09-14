package node

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/openaiapi"
)

// The durable half of not charging twice for one request.
//
// It is persisted rather than kept in memory because the failure it prevents
// outlives a process: a node that restarts between a charge and a retry would
// forget the key and charge again, which is exactly the case a buyer cannot see
// coming. It is small - one record per key per buyer, expired after a day - and
// it stores no prompt and no completion, only the fact that a key was used and
// which job used it.

// idempotencyKeyPrefix namespaces these records in the shared store.
const idempotencyKeyPrefix = "inference/idem/"

// IdempotencyTTL is how long a key is remembered.
//
// A day is long enough to cover any retry a client or a proxy performs - those
// happen in seconds - and short enough that the keyspace does not grow without
// bound. Past it a key is reusable, which is the same thing as a new key: the
// original charge is long settled and the buyer asking again is asking for new
// work.
const IdempotencyTTL = 24 * time.Hour

// kvIdempotencyStore is the openaiapi.IdempotencyStore backed by the node's
// store.
type kvIdempotencyStore struct {
	// mu serialises the read-then-write in Reserve. The store has no
	// compare-and-set, and without this two simultaneous retries of the same
	// request both read "not seen" and both charge - which is precisely the race
	// this whole mechanism exists to lose.
	mu    sync.Mutex
	store *kv.Store
	now   func() time.Time
}

// newIdempotencyStore builds the store.
func newIdempotencyStore(store *kv.Store) *kvIdempotencyStore {
	return &kvIdempotencyStore{store: store, now: time.Now}
}

// recordKey is the store key for one buyer's key.
//
// The buyer is part of it because an idempotency key is the CALLER's identifier
// and two callers have no reason to coordinate: without the buyer in the key,
// one buyer's "1" would silently block another's.
func (s *kvIdempotencyStore) recordKey(buyer, key string) []byte {
	// The separator cannot appear in an account id, so no pair of (buyer, key)
	// can collide with another by running together.
	return []byte(idempotencyKeyPrefix + buyer + "\x00" + key)
}

// Reserve records a key as in-flight, or returns what is already there.
func (s *kvIdempotencyStore) Reserve(buyer, key string, rec openaiapi.IdempotencyRecord) (openaiapi.IdempotencyRecord, bool, error) {
	if strings.TrimSpace(buyer) == "" || strings.TrimSpace(key) == "" {
		return openaiapi.IdempotencyRecord{}, false, fmt.Errorf("node: an idempotency record needs a buyer and a key")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	k := s.recordKey(buyer, key)
	data, err := s.store.Get(k)
	if err != nil {
		return openaiapi.IdempotencyRecord{}, false, fmt.Errorf("node: read the idempotency record: %w", err)
	}
	if data != nil {
		var existing openaiapi.IdempotencyRecord
		if err := json.Unmarshal(data, &existing); err == nil {
			// An expired record is treated as absent rather than deleted here: the
			// overwrite below replaces it, so expiry costs no separate sweep.
			if s.now().UTC().Sub(existing.CreatedAt) < IdempotencyTTL {
				return existing, true, nil
			}
		}
		// A record that will not decode is not something to act on. Replacing it is
		// the safe direction: refusing would make one corrupt byte permanently
		// block a key the buyer is entitled to use.
	}

	rec.CreatedAt = s.now().UTC()
	encoded, err := json.Marshal(rec)
	if err != nil {
		return openaiapi.IdempotencyRecord{}, false, fmt.Errorf("node: encode the idempotency record: %w", err)
	}
	if err := s.store.Put(k, encoded); err != nil {
		return openaiapi.IdempotencyRecord{}, false, fmt.Errorf("node: store the idempotency record: %w", err)
	}
	return rec, false, nil
}

// Complete marks the key's job as finished and charged.
func (s *kvIdempotencyStore) Complete(buyer, key, jobID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	k := s.recordKey(buyer, key)
	data, err := s.store.Get(k)
	if err != nil {
		return fmt.Errorf("node: read the idempotency record: %w", err)
	}
	var rec openaiapi.IdempotencyRecord
	if data != nil {
		_ = json.Unmarshal(data, &rec)
	}
	rec.JobID = jobID
	rec.Completed = true
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = s.now().UTC()
	}
	encoded, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("node: encode the idempotency record: %w", err)
	}
	return s.store.Put(k, encoded)
}

// Release forgets a key whose job failed.
//
// Deleting rather than marking failed, because the two are the same answer to
// the only question asked of this record - may this key run - and a delete needs
// no expiry of its own.
func (s *kvIdempotencyStore) Release(buyer, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.Delete(s.recordKey(buyer, key))
}
