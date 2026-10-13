package cachecloud

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"encoding/hex"
	"reflect"
	"time"

	"github.com/golang-acexy/cloud-cache/internal/stablehash"
	"github.com/zeebo/xxh3"
)

const cacheExpirationTolerance = 3 * time.Second

// cacheValueEnvelope stores Gob payload bytes with their hash and a millisecond deadline.
type cacheValueEnvelope struct {
	// Payload contains the Gob-encoded cache value.
	Payload []byte
	// ValueHash identifies the decoded value, with a tagged payload fallback.
	ValueHash string
	// ExpireAt is the logical expiration timestamp in Unix milliseconds.
	ExpireAt int64
}

func newCacheValueEnvelope(data any, expiration time.Duration) (cacheValueEnvelope, error) {
	var payload bytes.Buffer
	if err := gob.NewEncoder(&payload).Encode(data); err != nil {
		return cacheValueEnvelope{}, err
	}
	dataBytes := payload.Bytes()
	// Hash the stored representation: Gob may discard or transform input fields.
	decoded := reflect.New(reflect.TypeOf(data))
	if err := gob.NewDecoder(bytes.NewReader(dataBytes)).Decode(decoded.Interface()); err != nil {
		return cacheValueEnvelope{}, err
	}
	valueHash, err := stablehash.Sum(decoded.Elem().Interface())
	if err != nil {
		// Preserve Gob-compatible values outside the canonicalizer's type or resource limits.
		valueHash = "gob:" + xxh3Hex(dataBytes)
	} else {
		valueHash = "stable:" + valueHash
	}
	return cacheValueEnvelope{
		Payload:   dataBytes,
		ValueHash: valueHash,
		ExpireAt:  time.Now().Add(expiration).UnixMilli(),
	}, nil
}

func xxh3Hex(data []byte) string {
	sum128 := xxh3.Hash128(data)
	var sum [16]byte
	binary.BigEndian.PutUint64(sum[:8], sum128.Hi)
	binary.BigEndian.PutUint64(sum[8:], sum128.Lo)
	return hex.EncodeToString(sum[:])
}

func (e cacheValueEnvelope) decode(result any) error {
	return gob.NewDecoder(bytes.NewReader(e.Payload)).Decode(result)
}

func (e cacheValueEnvelope) expired(now time.Time) bool {
	return e.ExpireAt <= now.UnixMilli()
}

// extendExpiration requires a future capped deadline advancing by strictly more than three seconds.
func (e *cacheValueEnvelope) extendExpiration(remoteExpireAt int64, maxExpiration time.Duration, now time.Time) bool {
	targetExpireAt := min(remoteExpireAt, now.Add(maxExpiration).UnixMilli())
	if targetExpireAt <= now.UnixMilli() || targetExpireAt <= e.ExpireAt+cacheExpirationTolerance.Milliseconds() {
		return false
	}
	e.ExpireAt = targetExpireAt
	return true
}
