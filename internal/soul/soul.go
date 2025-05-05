package soul

import (
	"sync"
)

// Soul represents an individual soul instance
type Soul struct {
	ID        string
	memory    []MemoryEntry
	memoryMu  sync.RWMutex
	values    map[string]float64
	valuesMu  sync.RWMutex
	persona   Persona
	personaMu sync.RWMutex
}

// MemoryEntry represents a piece of soul memory
type MemoryEntry struct {
	Timestamp int64
	Content   string
	Type      string
	Tags      []string
}

// Persona represents a soul's personality traits
type Persona struct {
	Traits map[string]float64
	Goals  []string
}

// New creates a new Soul instance
func New(id string) *Soul {
	return &Soul{
		ID:     id,
		memory: make([]MemoryEntry, 0),
		values: make(map[string]float64),
		persona: Persona{
			Traits: make(map[string]float64),
			Goals:  make([]string, 0),
		},
	}
}

// AddMemory adds a new memory entry
func (s *Soul) AddMemory(entry MemoryEntry) {
	s.memoryMu.Lock()
	defer s.memoryMu.Unlock()
	s.memory = append(s.memory, entry)
}

// GetMemories returns all memories matching given tags
func (s *Soul) GetMemories(tags []string) []MemoryEntry {
	s.memoryMu.RLock()
	defer s.memoryMu.RUnlock()

	if len(tags) == 0 {
		result := make([]MemoryEntry, len(s.memory))
		copy(result, s.memory)
		return result
	}

	var matches []MemoryEntry
	for _, entry := range s.memory {
		if hasMatchingTags(entry.Tags, tags) {
			matches = append(matches, entry)
		}
	}
	return matches
}

// SetValue updates a soul value
func (s *Soul) SetValue(key string, value float64) {
	s.valuesMu.Lock()
	defer s.valuesMu.Unlock()
	s.values[key] = value
}

// GetValue retrieves a soul value
func (s *Soul) GetValue(key string) (float64, bool) {
	s.valuesMu.RLock()
	defer s.valuesMu.RUnlock()
	val, ok := s.values[key]
	return val, ok
}

// UpdatePersona updates the soul's persona
func (s *Soul) UpdatePersona(persona Persona) {
	s.personaMu.Lock()
	defer s.personaMu.Unlock()
	s.persona = persona
}

// GetPersona returns the soul's current persona
func (s *Soul) GetPersona() Persona {
	s.personaMu.RLock()
	defer s.personaMu.RUnlock()
	return s.persona
}

// hasMatchingTags checks if two tag slices share any elements
func hasMatchingTags(a, b []string) bool {
	for _, tag := range a {
		for _, target := range b {
			if tag == target {
				return true
			}
		}
	}
	return false
}
