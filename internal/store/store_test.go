package store

import (
	"testing"

	"github.com/adhikareeprayush/kv-store/internal/hlc"
)

func TestPutGet(t *testing.T) {
	s := New()
	ts := hlc.Timestamp{WallTime: 100, Logical: 1}
	s.Put("k", Value{Data: []byte("v"), Timestamp: ts})

	val, ok := s.Get("k")
	if !ok || string(val.Data) != "v" {
		t.Fatalf("unexpected get: ok=%v val=%+v", ok, val)
	}
}

func TestApplyLastWriteWins(t *testing.T) {
	s := New()
	older := hlc.Timestamp{WallTime: 100, Logical: 1}
	newer := hlc.Timestamp{WallTime: 100, Logical: 2}

	s.Put("k", Value{Data: []byte("new"), Timestamp: newer})
	if !s.Apply("k", Value{Data: []byte("old"), Timestamp: older}) {
		// rejected
	} else {
		t.Fatal("older write should be rejected")
	}

	val, ok := s.Get("k")
	if !ok || string(val.Data) != "new" {
		t.Fatalf("expected newer value, got %+v ok=%v", val, ok)
	}
}

func TestDeleteTombstone(t *testing.T) {
	s := New()
	ts := hlc.Timestamp{WallTime: 50, Logical: 0}
	s.Put("k", Value{Data: []byte("v"), Timestamp: ts})
	s.Delete("k", hlc.Timestamp{WallTime: 100, Logical: 0})

	if _, ok := s.Get("k"); ok {
		t.Fatal("expected key to be deleted")
	}
	ver, ok := s.GetVersion("k")
	if !ok || ver.WallTime != 100 {
		t.Fatalf("expected tombstone version, got %+v ok=%v", ver, ok)
	}
}

func TestGetVersionMissingKey(t *testing.T) {
	s := New()
	if _, ok := s.GetVersion("missing"); ok {
		t.Fatal("expected missing key")
	}
}
