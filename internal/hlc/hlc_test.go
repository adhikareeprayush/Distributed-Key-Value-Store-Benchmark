package hlc

import (
	"testing"
	"time"
)

func TestCompareOrdering(t *testing.T) {
	older := Timestamp{WallTime: 100, Logical: 1}
	newer := Timestamp{WallTime: 100, Logical: 2}
	muchNewer := Timestamp{WallTime: 200, Logical: 0}

	if Compare(older, newer) >= 0 {
		t.Fatal("expected older < newer")
	}
	if Compare(newer, muchNewer) >= 0 {
		t.Fatal("expected newer < muchNewer")
	}
	if Compare(older, older) != 0 {
		t.Fatal("expected equal timestamps")
	}
}

func TestSendMonotonic(t *testing.T) {
	c := New()
	ts1, err := c.Send()
	if err != nil {
		t.Fatal(err)
	}
	ts2, err := c.Send()
	if err != nil {
		t.Fatal(err)
	}
	if Compare(ts1, ts2) >= 0 {
		t.Fatalf("expected strictly increasing timestamps: %v then %v", ts1, ts2)
	}
}

func TestReceiveNewerRemote(t *testing.T) {
	now := time.Now().UnixNano()
	c := &Clock{l: now - 100, c: 2}
	remote := Timestamp{WallTime: now + int64(time.Second), Logical: 4}

	ts, err := c.Receive(remote)
	if err != nil {
		t.Fatal(err)
	}
	want := Timestamp{WallTime: remote.WallTime, Logical: 5}
	if ts != want {
		t.Fatalf("got %+v, want %+v", ts, want)
	}
}

func TestReceiveRejectsNegativeLogical(t *testing.T) {
	c := New()
	if _, err := c.Receive(Timestamp{WallTime: 1, Logical: -1}); err == nil {
		t.Fatal("expected error for negative logical counter")
	}
}
