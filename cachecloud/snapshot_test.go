package cachecloud

import (
	"bytes"
	"encoding/gob"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/acexy/golang-toolkit/caching"
	"github.com/allegro/bigcache/v3"
)

var snapshotEncodes atomic.Int32

type snapshotTransform struct{ Value string }

// GobEncode counts business encodings without changing their deterministic representation.
func (v snapshotTransform) GobEncode() ([]byte, error) {
	snapshotEncodes.Add(1)
	return []byte(v.Value), nil
}

// GobDecode changes the observable value to detect accidental re-encoding of decoded objects.
func (v *snapshotTransform) GobDecode(data []byte) error {
	if string(data) == "reject" {
		return errors.New("snapshot decode rejected")
	}
	v.Value = "decoded:" + string(data)
	return nil
}

func TestSingleflightSnapshotStorage(t *testing.T) {
	resetRuntimeForTest(t)
	topic := useTestTopics(t)
	settings := bigcache.Config{Shards: 1, MaxEntriesInWindow: 16, MaxEntrySize: 128}
	if err := Init(Options{ServiceName: "snapshot"},
		NewMemBucketConfig("mem", time.Minute, settings),
		NewDistMemBucketConfig("dist", time.Minute, settings),
		NewLevel2BucketConfig("l2", time.Minute, 2*time.Minute, settings)); err != nil {
		t.Fatal(err)
	}
	rt := loadRuntime()
	var l2Stored cacheValueEnvelope
	rt.level2.buckets["l2"].redisBucket = &testRedisBucket{put: func(_ CacheKey, v any, _ ...any) error { l2Stored = v.(cacheValueEnvelope); return nil }}
	for _, name := range []BucketName{"mem", "dist", "l2"} {
		t.Run(string(name), func(t *testing.T) {
			snapshotEncodes.Store(0)
			var result snapshotTransform
			key := NewCacheKey("item:%s")
			err := CacheableSingleflight(name, key, &result, func(out *snapshotTransform) (bool, error) { out.Value = "input"; return true, nil }, "literal:%s")
			if err != nil || result.Value != "decoded:input" {
				t.Fatalf("loaded: %+v %v", result, err)
			}
			if snapshotEncodes.Load() != 1 {
				t.Fatalf("business encodings=%d", snapshotEncodes.Load())
			}
			var cached snapshotTransform
			if err := Get(name, key, &cached, "literal:%s"); err != nil || cached != result {
				t.Fatalf("stored snapshot: %+v %v", cached, err)
			}
			if name != "mem" {
				var envelope cacheValueEnvelope
				if name == "dist" {
					b := rt.distMem.buckets[name]
					if err := b.manager.Get(b.managerBucketName, caching.NewCacheKey("item:literal:%s"), &envelope); err != nil {
						t.Fatal(err)
					}
				} else {
					envelope = l2Stored
				}
				var legacy snapshotTransform
				if err := gob.NewDecoder(bytes.NewReader(envelope.Payload)).Decode(&legacy); err != nil || legacy != result {
					t.Fatalf("legacy decode: %+v %v", legacy, err)
				}
				want, err := newCacheValueEnvelope(&snapshotTransform{Value: "input"}, time.Minute)
				if err != nil || want.ValueHash != envelope.ValueHash {
					t.Fatalf("legacy digest: %s %s %v", want.ValueHash, envelope.ValueHash, err)
				}
			}
			before := len(topic.events)
			var rejected snapshotTransform
			err = CacheableSingleflight(name, NewCacheKey("reject"), &rejected, func(out *snapshotTransform) (bool, error) { out.Value = "reject"; return true, nil })
			if err == nil {
				t.Fatal("invalid snapshot accepted")
			}
			if len(topic.events) != before {
				t.Fatal("invalid snapshot published")
			}
			var got snapshotTransform
			if err := Get(name, NewCacheKey("reject"), &got); !errors.Is(err, ErrCacheMiss) {
				t.Fatalf("invalid snapshot stored: %v", err)
			}
		})
	}
}

func TestSnapshotEnvelopeCompatibility(t *testing.T) {
	values := []any{(*snapshotTransform)(nil), []int(nil), map[string]int(nil), map[string]int{"b": 2, "a": 1}, snapshotTransform{Value: "input"}, struct {
		Public  string
		private int
	}{"value", 1}}
	for _, v := range values {
		payload, err := encodeCacheValue(v)
		if err != nil {
			t.Fatal(err)
		}
		decoded := reflect.New(reflect.TypeOf(v))
		if err := decodeCacheValue(payload, decoded.Interface()); err != nil {
			t.Fatal(err)
		}
		envelope := cacheEnvelopeFromSnapshot(payload, decoded.Interface(), time.Minute)
		legacy, err := newCacheValueEnvelope(v, time.Minute)
		if err != nil || envelope.ValueHash != legacy.ValueHash {
			t.Fatalf("digest mismatch %T: %v", v, err)
		}
		if len(payload) > 0 && &envelope.Payload[0] != &payload[0] {
			t.Fatal("payload was copied")
		}
		var outer bytes.Buffer
		if err := gob.NewEncoder(&outer).Encode(envelope); err != nil {
			t.Fatal(err)
		}
		var wire cacheValueEnvelope
		if err := gob.NewDecoder(&outer).Decode(&wire); err != nil {
			t.Fatal(err)
		}
		out := reflect.New(reflect.TypeOf(v))
		if err := wire.decode(out.Interface()); err != nil || !reflect.DeepEqual(out.Elem().Interface(), decoded.Elem().Interface()) {
			t.Fatalf("legacy envelope round trip %T: %v", v, err)
		}
	}
}

func TestSingleflightSnapshotL2WriteFailure(t *testing.T) {
	resetRuntimeForTest(t)
	topic := useTestTopics(t)
	settings := bigcache.Config{Shards: 1, MaxEntriesInWindow: 16, MaxEntrySize: 128}
	manager, err := newLevel2CacheManager("snapshot-errors", []BucketConfig{NewLevel2BucketConfig("l2", time.Minute, time.Hour, settings)})
	if err != nil {
		t.Fatal(err)
	}
	expected := errors.New("L2 write failed")
	writes := 0
	manager.buckets["l2"].redisBucket = &testRedisBucket{put: func(_ CacheKey, v any, _ ...any) error {
		writes++
		envelope := v.(cacheValueEnvelope)
		var value snapshotTransform
		if err := envelope.decode(&value); err != nil || value.Value != "decoded:input" {
			t.Fatalf("snapshot passed to L2: %+v %v", value, err)
		}
		return expected
	}}
	runtimeState.Lock()
	runtimeState.instance = &cacheRuntime{level2: manager}
	runtimeState.Unlock()
	snapshotEncodes.Store(0)
	var result snapshotTransform
	err = CacheableSingleflight("l2", NewCacheKey("failed"), &result, func(out *snapshotTransform) (bool, error) { out.Value = "input"; return true, nil })
	if !errors.Is(err, expected) || result.Value != "decoded:input" || writes != 1 || snapshotEncodes.Load() != 1 {
		t.Fatalf("write failure: result=%+v writes=%d encodes=%d err=%v", result, writes, snapshotEncodes.Load(), err)
	}
	if len(topic.events) != 0 {
		t.Fatal("failed L2 write published an event")
	}
	var cached snapshotTransform
	if err := Get("l2", NewCacheKey("failed"), &cached); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("failed L2 write filled L1: %v", err)
	}
}
