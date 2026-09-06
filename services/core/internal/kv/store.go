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
