package buffer

import (
	"sync"

	"github.com/adhikareeprayush/kv-store/internal/hlc"
	"github.com/adhikareeprayush/kv-store/internal/store"
)

type pendingWrite struct {
	key string
	val store.Value
}

type Buffer struct {
	mu    sync.Mutex
	store *store.Store
	held  []pendingWrite
}

func New(s *store.Store) *Buffer {
	return &Buffer{store: s}
}

// Receive either applies the write immediately or holds it until dependencies are satisfied.
// Returns true when the write was applied now (not buffered).
func (b *Buffer) Receive(key string, val store.Value) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.depsSatisfied(val.Deps) {
		b.applyLocked(key, val)
		b.drainLocked()
		return true
	}
	b.held = append(b.held, pendingWrite{key: key, val: val})
	return false
}

func (b *Buffer) depsSatisfied(deps []store.Dependency) bool {
	for _, dep := range deps {
		ts, ok := b.store.GetVersion(dep.Key)
		if !ok {
			ts = hlc.Timestamp{}
		}
		if !ts.AfterOrEqual(dep.MinTS) {
			return false
		}
	}
	return true
}

func (b *Buffer) applyLocked(key string, val store.Value) {
	if val.Deleted {
		b.store.ApplyDelete(key, val.Timestamp)
		return
	}
	b.store.Apply(key, val)
}

func (b *Buffer) drainLocked() {
	for {
		progressed := false
		remaining := b.held[:0]
		for _, p := range b.held {
			if b.depsSatisfied(p.val.Deps) {
				b.applyLocked(p.key, p.val)
				progressed = true
			} else {
				remaining = append(remaining, p)
			}
		}
		b.held = remaining
		if !progressed {
			return
		}
	}
}

// Len returns the number of held writes (for tests).
func (b *Buffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.held)
}
