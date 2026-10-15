package cachecloud

import (
	"bytes"
	"encoding/gob"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/acexy/golang-toolkit/caching"
)

func TestNilCacheValues(t *testing.T) {
	resetRuntimeForTest(t)
	if err := Init(Options{ServiceName: "nil"}, NewMemBucketConfig("memory", time.Minute)); err != nil {
		t.Fatal(err)
	}
	var pointer *hashTestValue
	var boxed any = pointer
	for _, tc := range []struct {
		name string
		data any
	}{
		{"nil", nil},
		{"pointer", pointer},
		{"pointer chain", &pointer},
		{"interface chain", &boxed},
		{"slice", []int(nil)},
		{"map", map[string]int(nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := NewCacheKey(tc.name)
			if err := Put("memory", key, tc.data); err != nil {
				t.Fatal(err)
			}
			result := &hashTestValue{Name: "stale"}
			if err := Get("memory", key, &result); err != nil || result != nil {
				t.Fatalf("nil hit: result=%v err=%v", result, err)
			}
			envelope, err := newCacheValueEnvelope(tc.data, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if len(envelope.Payload) != 0 || envelope.ValueHash != "nil" || envelope.expired(time.Now()) {
				t.Fatalf("invalid nil envelope: %+v", envelope)
			}
			result = &hashTestValue{Name: "stale"}
			if err := envelope.decode(&result); err != nil || result != nil {
				t.Fatalf("nil envelope hit: result=%v err=%v", result, err)
			}
			if err := Evict("memory", key); err != nil {
				t.Fatal(err)
			}
			if err := Get("memory", key, &result); !errors.Is(err, ErrCacheMiss) {
				t.Fatalf("deleted nil must miss: %v", err)
			}
		})
	}
}

func TestCacheableNilResults(t *testing.T) {
	resetRuntimeForTest(t)
	if err := Init(Options{ServiceName: "nil"}, NewMemBucketConfig("memory", time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, flight := range []bool{false, true} {
		for _, store := range []bool{false, true} {
			key := NewCacheKey("key:%t:%t").RawKeyString(flight, store)
			calls := 0
			loader := func(result **hashTestValue) (bool, error) {
				calls++
				*result = nil
				return store, nil
			}
			for range 2 {
				result := &hashTestValue{Name: "stale"}
				var err error
				if flight {
					err = CacheableSingleflight("memory", NewCacheKey(key), &result, loader)
				} else {
					err = Cacheable("memory", NewCacheKey(key), &result, loader)
				}
				if err != nil || result != nil {
					t.Fatalf("flight=%t store=%t result=%v err=%v", flight, store, result, err)
				}
			}
			want := 2
			if store {
				want = 1
			}
			if calls != want {
				t.Fatalf("flight=%t store=%t loader calls=%d want=%d", flight, store, calls, want)
			}
		}
	}
}

func TestCacheCodecCompatibility(t *testing.T) {
	for _, value := range []any{"", 0, false, []int{}, map[string]int{}, hashTestValue{Name: "value"}} {
		var legacy bytes.Buffer
		if err := gob.NewEncoder(&legacy).Encode(value); err != nil {
			t.Fatal(err)
		}
		payload, err := encodeCacheValue(value)
		if err != nil || !bytes.Equal(payload, legacy.Bytes()) {
			t.Fatalf("non-nil wire format changed for %T: %v", value, err)
		}
	}
	if err := decodeCacheValue([]byte("invalid gob"), new(string)); err == nil {
		t.Fatal("malformed non-empty payload must fail")
	}
	for _, result := range []any{nil, (*string)(nil), "not a pointer"} {
		if err := decodeCacheValue(nil, result); err == nil {
			t.Fatalf("invalid destination accepted: %T", result)
		}
	}
	result := "stale"
	if err := decodeCacheValue(nil, &result); err != nil || result != "" {
		t.Fatalf("nil must clear scalar destination: %q %v", result, err)
	}
}

func TestDistributedNilValues(t *testing.T) {
	topic := useTestTopics(t)
	dist := newDistMemSyncTestBucket(t)
	l2 := newLevel2SyncTestBucket(t)
	var stored cacheValueEnvelope
	reads := 0
	l2.redisBucket = &testRedisBucket{
		put: func(_ CacheKey, data any, _ ...any) error {
			stored = data.(cacheValueEnvelope)
			return nil
		},
		get: func(_ CacheKey, result any, _ ...any) error {
			reads++
			*result.(*cacheValueEnvelope) = stored
			return nil
		},
	}
	for _, bucket := range []CacheBucket{dist, l2} {
		key := NewCacheKey("nil")
		if err := bucket.Put(key, nil); err != nil {
			t.Fatal(err)
		}
		result := &hashTestValue{Name: "stale"}
		if err := bucket.Get(key, &result); err != nil || result != nil {
			t.Fatalf("distributed nil hit: %v %v", result, err)
		}
	}
	if len(topic.events) != 2 || topic.events[0].ValueHash != topic.events[1].ValueHash {
		t.Fatalf("nil writes must publish matching hashes: %+v", topic.events)
	}
	if err := l2.memoryManager.Evict(l2.memoryBucketName, caching.NewCacheKey("nil")); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		result := &hashTestValue{Name: "stale"}
		if err := l2.Get(NewCacheKey("nil"), &result); err != nil || result != nil {
			t.Fatalf("L2 nil rebuild/hit: %v %v", result, err)
		}
	}
	if reads != 1 {
		t.Fatalf("nil L2 value must refill L1: reads=%d", reads)
	}
	dist.applySyncEvent(cacheSyncEvent{Operation: cacheSyncPut, CacheKey: "nil", ValueHash: "different", ExpireAt: time.Now().Add(time.Minute).UnixMilli()})
	var result *hashTestValue
	if err := dist.Get(NewCacheKey("nil"), &result); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("different remote value must invalidate nil: %v", err)
	}
}

func TestSingleflightSharesNil(t *testing.T) {
	resetRuntimeForTest(t)
	if err := Init(Options{ServiceName: "nil"}, NewMemBucketConfig("memory", time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, store := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			release := make(chan struct{})
			calls := 0
			loader := func(result **hashTestValue) (bool, error) {
				calls++
				<-release
				*result = nil
				return store, nil
			}
			const n = 8
			results := make([]*hashTestValue, n)
			errs := make([]error, n)
			key := NewCacheKey("nil:%t").RawKeyString(store)
			for i := range n {
				results[i] = &hashTestValue{Name: "stale"}
				go func() {
					errs[i] = CacheableSingleflight("memory", NewCacheKey(key), &results[i], loader)
				}()
			}
			synctest.Wait()
			if calls != 1 {
				t.Fatalf("loader calls=%d", calls)
			}
			close(release)
			synctest.Wait()
			for i := range n {
				if errs[i] != nil || results[i] != nil {
					t.Fatalf("waiter %d: %v %v", i, results[i], errs[i])
				}
			}
		})
	}
}
