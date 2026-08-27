package store

import (
	"sync"
	"github.com/adhikareeprayush/kv-store/internal/hlc"
)

type Dependency struct {
	Key   string
	MinTS hlc.Timestamp
}

type Value struct {
	Data      []byte
	Timestamp hlc.Timestamp
	Deps      []Dependency // empty in eventual/strong modes
	Deleted   bool
}

type Store struct {
	mu   sync.RWMutex
	data map[string]Value
}

func New() *Store {
	return &Store{data: make(map[string]Value)}
}

// Put unconditionally writes a value (local client writes).
func (s *Store) Put(key string, val Value) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = val
}

// Apply applies a replicated write using last-write-wins on the HLC timestamp.
// Returns true if the write was accepted.
func (s *Store) Apply(key string, val Value) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if cur, ok := s.data[key]; ok {
		if hlc.Compare(val.Timestamp, cur.Timestamp) <= 0 {
			return false
		}
	}
	s.data[key] = val
	return true
}

func (s *Store) Get(key string) (Value, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	val, ok := s.data[key]
	if !ok || val.Deleted {
		return Value{}, false
	}
	return val, true
}

// GetVersion returns the stored version for a key, including tombstones.
// Missing keys report (zero timestamp, false).
func (s *Store) GetVersion(key string) (hlc.Timestamp, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	val, ok := s.data[key]
	if !ok {
		return hlc.Timestamp{}, false
	}
	return val.Timestamp, true
}

func (s *Store) Delete(key string, ts hlc.Timestamp) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = Value{Timestamp: ts, Deleted: true}
}

func (s *Store) ApplyDelete(key string, ts hlc.Timestamp) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	val := Value{Timestamp: ts, Deleted: true}
	if cur, ok := s.data[key]; ok {
		if hlc.Compare(ts, cur.Timestamp) <= 0 {
			return false
		}
	}
	s.data[key] = val
	return true
}
