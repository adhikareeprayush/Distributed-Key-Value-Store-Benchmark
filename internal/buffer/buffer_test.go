package buffer

import (
	"testing"

	"github.com/adhikareeprayush/kv-store/internal/hlc"
	"github.com/adhikareeprayush/kv-store/internal/store"
)

func TestReceiveBuffersUntilDependencyMet(t *testing.T) {
	s := store.New()
	buf := New(s)

	depTS := hlc.Timestamp{WallTime: 100, Logical: 0}
	writeTS := hlc.Timestamp{WallTime: 200, Logical: 0}

	blocked := store.Value{
		Data:      []byte("y"),
		Timestamp: writeTS,
		Deps: []store.Dependency{
			{Key: "x", MinTS: depTS},
		},
	}

	if buf.Receive("y", blocked) {
		t.Fatal("expected write to be buffered")
	}
	if buf.Len() != 1 {
		t.Fatalf("expected 1 held write, got %d", buf.Len())
	}

	s.Put("x", store.Value{Data: []byte("x"), Timestamp: depTS})
	buf.Receive("wake", store.Value{
		Data:      []byte("wake"),
		Timestamp: hlc.Timestamp{WallTime: 300, Logical: 0},
	})

	val, ok := s.Get("y")
	if !ok || string(val.Data) != "y" {
		t.Fatalf("expected buffered write to be applied, got ok=%v val=%+v", ok, val)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected empty buffer, got %d", buf.Len())
	}
}

func TestReceiveAppliesWhenNoDeps(t *testing.T) {
	s := store.New()
	buf := New(s)

	if !buf.Receive("k", store.Value{
		Data:      []byte("v"),
		Timestamp: hlc.Timestamp{WallTime: 1, Logical: 0},
	}) {
		t.Fatal("expected immediate apply")
	}
	if _, ok := s.Get("k"); !ok {
		t.Fatal("expected key in store")
	}
}
