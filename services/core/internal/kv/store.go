package kv

import (
	"fmt"
	"sync"

	"github.com/cockroachdb/pebble"
)

// Store represents a key-value store
type Store struct {
	db      *pebble.DB
	writeMu sync.RWMutex
}

// Config represents store configuration
type Config struct {
	Path string
}

// New creates a new Store instance
func New(cfg Config) (*Store, error) {
	// Open Pebble database
	db, err := pebble.Open(cfg.Path, &pebble.Options{})
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	return &Store{
		db: db,
	}, nil
}

// Get retrieves a value by key
func (s *Store) Get(key []byte) ([]byte, error) {
	s.writeMu.RLock()
	defer s.writeMu.RUnlock()

	value, closer, err := s.db.Get(key)
	if err == pebble.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get key: %w", err)
	}
	defer closer.Close()

	// Copy value since it's only valid until closer.Close()
	result := make([]byte, len(value))
	copy(result, value)
	return result, nil
}

// Put stores a key-value pair
func (s *Store) Put(key, value []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if err := s.db.Set(key, value, pebble.Sync); err != nil {
		return fmt.Errorf("failed to set key: %w", err)
	}
	return nil
}

// Delete removes a key-value pair
func (s *Store) Delete(key []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if err := s.db.Delete(key, pebble.Sync); err != nil {
		return fmt.Errorf("failed to delete key: %w", err)
	}
	return nil
}

// NewBatch creates a new write batch
func (s *Store) NewBatch() *pebble.Batch {
	return s.db.NewBatch()
}

// Iterate scans every key that begins with prefix in lexicographic order and
// invokes fn with a copy of each key and value. The slices passed to fn are
// owned by the caller and remain valid after fn returns. Iteration stops early
// and returns the first error fn produces. A nil or empty prefix scans the
// whole store.
func (s *Store) Iterate(prefix []byte, fn func(key, value []byte) error) error {
	s.writeMu.RLock()
	defer s.writeMu.RUnlock()

	opts := &pebble.IterOptions{}
	if len(prefix) > 0 {
		opts.LowerBound = prefix
		opts.UpperBound = prefixUpperBound(prefix)
	}

	iter, err := s.db.NewIter(opts)
	if err != nil {
		return fmt.Errorf("failed to create iterator: %w", err)
	}
	defer iter.Close()

	for iter.First(); iter.Valid(); iter.Next() {
		// Copy key and value since they are only valid until the iterator moves.
		k := make([]byte, len(iter.Key()))
		copy(k, iter.Key())
		v := make([]byte, len(iter.Value()))
		copy(v, iter.Value())
		if err := fn(k, v); err != nil {
			return err
		}
	}
	if err := iter.Error(); err != nil {
		return fmt.Errorf("iterator error: %w", err)
	}
	return nil
}

// prefixUpperBound returns the smallest key that is strictly greater than every
// key beginning with prefix, giving Iterate an exclusive upper bound. It returns
// nil when no such bound exists (prefix is all 0xff bytes), meaning "no upper
// bound".
func prefixUpperBound(prefix []byte) []byte {
	upper := make([]byte, len(prefix))
	copy(upper, prefix)
	for i := len(upper) - 1; i >= 0; i-- {
		if upper[i] != 0xff {
			upper[i]++
			return upper[:i+1]
		}
	}
	return nil
}

// Close shuts down the store
func (s *Store) Close() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if err := s.db.Close(); err != nil {
		return fmt.Errorf("failed to close database: %w", err)
	}
	return nil
}

// Snapshot creates a consistent point-in-time snapshot
func (s *Store) Snapshot() (*pebble.Snapshot, error) {
	s.writeMu.RLock()
	defer s.writeMu.RUnlock()

	return s.db.NewSnapshot(), nil
}
