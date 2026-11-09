package core

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"
)

// ULID implementation (Crockford base32, 48-bit ms timestamp + 80-bit entropy).
// Kept dependency-free so P0 adds no module that docs/10 has not reviewed.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// Within one millisecond ULIDs must be strictly monotonic, otherwise cursor
// pagination and crash-recovery ordering are not stable (docs/06 §3).
var ulidState = struct {
	mu       sync.Mutex
	lastMS   uint64
	entropy  [10]byte
	hasState bool
}{}

// NewID returns a lexicographically sortable ULID used for tasks.id.
func NewID() TaskID {
	ms := uint64(time.Now().UnixMilli())
	ulidState.mu.Lock()
	if !ulidState.hasState || ms != ulidState.lastMS {
		if _, err := rand.Read(ulidState.entropy[:]); err != nil {
			ulidState.mu.Unlock()
			panic("core: crypto/rand unavailable: " + err.Error())
		}
		ulidState.lastMS = ms
		ulidState.hasState = true
	} else {
		incrementEntropy(ulidState.entropy[:])
	}
	var b [16]byte
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	copy(b[6:], ulidState.entropy[:])
	ulidState.mu.Unlock()
	return TaskID(encodeULID(b))
}

func incrementEntropy(e []byte) {
	for i := len(e) - 1; i >= 0; i-- {
		e[i]++
		if e[i] != 0 {
			return
		}
	}
	// Entropy overflow within the same millisecond is astronomically unlikely;
	// roll the timestamp forward by one millisecond rather than emit a duplicate.
	ulidState.lastMS++
}

func encodeULID(b [16]byte) string {
	n := new(big.Int).SetBytes(b[:])
	base := big.NewInt(32)
	var out [26]byte
	for i := 25; i >= 0; i-- {
		q, r := new(big.Int).QuoRem(n, base, new(big.Int))
		out[i] = crockford[r.Int64()]
		n = q
	}
	return string(out[:])
}

// ParseID validates a ULID and returns its embedded creation time.
func ParseID(id TaskID) (time.Time, error) {
	s := strings.ToUpper(string(id))
	if len(s) != 26 {
		return time.Time{}, fmt.Errorf("invalid ULID length %d", len(s))
	}
	n := new(big.Int)
	base := big.NewInt(32)
	for _, c := range s {
		p := strings.IndexByte(crockford, byte(c))
		if p < 0 {
			return time.Time{}, fmt.Errorf("invalid ULID character %q", c)
		}
		n.Mul(n, base)
		n.Add(n, big.NewInt(int64(p)))
	}
	raw := n.Bytes()
	if len(raw) > 16 {
		return time.Time{}, fmt.Errorf("invalid ULID value")
	}
	var b [16]byte
	copy(b[16-len(raw):], raw)
	ms := uint64(b[0])<<40 | uint64(b[1])<<32 | uint64(b[2])<<24 |
		uint64(b[3])<<16 | uint64(b[4])<<8 | uint64(b[5])
	return time.UnixMilli(int64(ms)).UTC(), nil
}

// TimeFromID is a convenience wrapper used by cursor pagination.
func TimeFromID(id TaskID) int64 {
	t, err := ParseID(id)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

// IDFromInt64 is used only by tests that need deterministic fixtures.
func IDFromInt64(v int64) TaskID {
	var b [16]byte
	binary.BigEndian.PutUint64(b[8:], uint64(v))
	return TaskID(encodeULID(b))
}
