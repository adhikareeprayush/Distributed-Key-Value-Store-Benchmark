package hlc

import (
	"errors"
	"sync"
	"time"
)

const maxLogical int32 = 2147483647

type Timestamp struct {
	WallTime int64
	Logical  int32
}

// Compare returns -1 if a < b, 0 if equal, 1 if a > b.
func Compare(a, b Timestamp) int {
	if a.WallTime < b.WallTime {
		return -1
	}
	if a.WallTime > b.WallTime {
		return 1
	}
	if a.Logical < b.Logical {
		return -1
	}
	if a.Logical > b.Logical {
		return 1
	}
	return 0
}

func (t Timestamp) Before(other Timestamp) bool { return Compare(t, other) < 0 }
func (t Timestamp) After(other Timestamp) bool  { return Compare(t, other) > 0 }
func (t Timestamp) Equal(other Timestamp) bool  { return Compare(t, other) == 0 }
func (t Timestamp) AfterOrEqual(other Timestamp) bool {
	return Compare(t, other) >= 0
}

type Clock struct {
	mu sync.Mutex
	l  int64
	c  int32
}

func New() *Clock {
	now := wallNow()
	return &Clock{l: now, c: 0}
}

func wallNow() int64 {
	return time.Now().UnixNano()
}

func (c *Clock) Now() Timestamp {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Timestamp{WallTime: c.l, Logical: c.c}
}

// Generate a new timestamp for attaching to a value
func (c *Clock) Send() (Timestamp, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := wallNow()
	lPrime := max64(c.l, now)

	if lPrime == c.l {
		if c.c >= maxLogical {
			return Timestamp{}, errors.New("hlc: logical counter overflow")
		}
		c.c++
	} else {
		c.c = 0
	}
	c.l = lPrime

	return Timestamp{WallTime: c.l, Logical: c.c}, nil
}

func (c *Clock) Receive(msg Timestamp) (Timestamp, error) {
	if msg.Logical < 0 {
		return Timestamp{}, errors.New("hlc: invalid logical counter")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	now := wallNow()
	lPrime := max64(c.l, msg.WallTime, now)

	switch lPrime {
	case c.l:
		if c.c >= maxLogical {
			return Timestamp{}, errors.New("hlc: logical counter overflow")
		}
		c.c++
	case msg.WallTime:
		if msg.Logical >= maxLogical {
			return Timestamp{}, errors.New("hlc: logical counter overflow")
		}
		c.c = msg.Logical + 1
	default:
		c.c = 0
	}
	c.l = lPrime

	return Timestamp{WallTime: c.l, Logical: c.c}, nil
}

func max64(vals ...int64) int64 {
	m := vals[0]
	for _, v := range vals[1:] {
		if v > m {
			m = v
		}
	}
	return m
}
