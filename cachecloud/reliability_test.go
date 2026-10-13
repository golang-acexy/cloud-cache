package cachecloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/acexy/golang-toolkit/caching"
	toolkitError "github.com/acexy/golang-toolkit/error"
	"github.com/allegro/bigcache/v3"
	"github.com/golang-acexy/starter-redis/redisstarter"
	"github.com/redis/go-redis/v9"
)

type testSyncTopic struct {
	events []cacheSyncEvent
	err    error
}

func (s *testSyncTopic) Publish(_ redisstarter.RedisKey, payload string, _ ...any) error {
	var event cacheSyncEvent
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return err
	}
	s.events = append(s.events, event)
	return s.err
}
func (*testSyncTopic) SubscribeRetry(context.Context, redisstarter.RedisKey, func(*redis.Message)) {}

func useTestTopics(t *testing.T) *testSyncTopic {
	t.Helper()
	oldL2, oldDist := level2SyncCmd, distMemSyncCmd
	topic := &testSyncTopic{}
	level2SyncCmd, distMemSyncCmd = topic, topic
	t.Cleanup(func() { level2SyncCmd, distMemSyncCmd = oldL2, oldDist })
	return topic
}

type testRedisBucket struct {
	get   func(CacheKey, any, ...any) error
	put   func(CacheKey, any, ...any) error
	evict func(CacheKey, ...any) error
}

func (b *testRedisBucket) Get(k CacheKey, v any, a ...any) error {
	if b.get != nil {
		return b.get(k, v, a...)
	}
	return ErrCacheMiss
}
func (b *testRedisBucket) Put(k CacheKey, v any, a ...any) error {
	if b.put != nil {
		return b.put(k, v, a...)
	}
	return nil
}
func (b *testRedisBucket) Evict(k CacheKey, a ...any) error {
	if b.evict != nil {
		return b.evict(k, a...)
	}
	return nil
}

type failingMemory struct {
	caching.CacheBucket
	writeErr, deleteErr error
	deletes             int
}

func (m *failingMemory) PutBytes(caching.CacheKey, []byte, ...any) error { return m.writeErr }
func (m *failingMemory) Evict(k caching.CacheKey, a ...any) error {
	m.deletes++
	if m.deleteErr != nil {
		return m.deleteErr
	}
	return m.CacheBucket.Evict(k, a...)
}

func TestCacheNamesRejectDelimiters(t *testing.T) {
	for _, name := range []string{"bad:name", "bad%name"} {
		if err := validateConfig(Options{ServiceName: name}, []BucketConfig{NewMemBucketConfig("valid", time.Minute)}); !errors.Is(err, ErrInvalidCacheName) {
			t.Fatalf("service: %v", err)
		}
		if err := validateConfig(Options{ServiceName: "valid"}, []BucketConfig{NewMemBucketConfig(BucketName(name), time.Minute)}); !errors.Is(err, ErrInvalidCacheName) {
			t.Fatalf("bucket: %v", err)
		}
	}
}

func TestSynchronizedKeysResolveOnceAndRejectEmpty(t *testing.T) {
	topic := useTestTopics(t)
	dist := newDistMemSyncTestBucket(t)
	l2 := newLevel2SyncTestBucket(t)
	redisCalls := 0
	l2.redisBucket = &testRedisBucket{put: func(k CacheKey, _ any, args ...any) error {
		redisCalls++
		if k.KeyFormat != "item:a:%s" || len(args) != 0 {
			t.Fatalf("unexpected resolved key %q, args %v", k.KeyFormat, args)
		}
		return nil
	}}
	for _, bucket := range []CacheBucket{dist, l2} {
		var got string
		for _, key := range []CacheKey{NewCacheKey(""), NewCacheKey("%s")} {
			var args []any
			if key.KeyFormat != "" {
				args = []any{""}
			}
			if err := bucket.Get(key, &got, args...); !errors.Is(err, ErrCacheKeyRequired) {
				t.Fatalf("get empty: %v", err)
			}
			if err := bucket.Put(key, "value", args...); !errors.Is(err, ErrCacheKeyRequired) {
				t.Fatalf("put empty: %v", err)
			}
			if err := bucket.Evict(key, args...); !errors.Is(err, ErrCacheKeyRequired) {
				t.Fatalf("evict empty: %v", err)
			}
		}
	}
	if redisCalls != 0 || len(topic.events) != 0 {
		t.Fatal("invalid keys modified state or published")
	}
	for _, bucket := range []CacheBucket{dist, l2} {
		if err := bucket.Put(NewCacheKey("item:%s"), "value", "a:%s"); err != nil {
			t.Fatal(err)
		}
		var got string
		if err := bucket.Get(NewCacheKey("item:%s"), &got, "a:%s"); err != nil || got != "value" {
			t.Fatalf("get literal formatted key: %q %v", got, err)
		}
	}
	for _, e := range topic.events {
		if e.CacheKey != "item:a:%s" {
			t.Fatalf("event key: %q", e.CacheKey)
		}
	}
}

func TestMemoryEvictionIsIdempotentAndPreservesFailures(t *testing.T) {
	manager, err := newMemoryCacheManager([]BucketConfig{NewMemBucketConfig("test", time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	bucket := manager.getBucket("test")
	for range 2 {
		if err := bucket.Evict(NewCacheKey("missing")); err != nil {
			t.Fatal(err)
		}
	}
	backendErr := errors.New("delete failed")
	backend := &failingMemory{CacheBucket: bucket.manager.GetBucket(bucket.bucketName), deleteErr: backendErr}
	bucket.manager = caching.NewCacheManager().AddBucket(bucket.bucketName, backend)
	if err := bucket.Evict(NewCacheKey("missing")); !errors.Is(err, backendErr) {
		t.Fatalf("lost failure: %v", err)
	}
	backend.deleteErr = fmt.Errorf("wrapped: %w", bigcache.ErrEntryNotFound)
	if err := bucket.Evict(NewCacheKey("missing")); err != nil {
		t.Fatal(err)
	}
}

func TestSynchronizedEvictionPublishesForMissingEntries(t *testing.T) {
	topic := useTestTopics(t)
	dist := newDistMemSyncTestBucket(t)
	l2 := newLevel2SyncTestBucket(t)
	l2.redisBucket = &testRedisBucket{}
	for _, bucket := range []CacheBucket{dist, l2} {
		for range 2 {
			if err := bucket.Evict(NewCacheKey("absent")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(topic.events) != 4 {
		t.Fatalf("events=%d", len(topic.events))
	}
	for _, event := range topic.events {
		if event.Operation != cacheSyncDelete {
			t.Fatal("expected delete")
		}
	}
}

func TestLevel2FailedLocalWriteClearsOldValueAndPublishes(t *testing.T) {
	for _, failDelete := range []bool{false, true} {
		t.Run(fmt.Sprint(failDelete), func(t *testing.T) {
			topic := useTestTopics(t)
			bucket := newLevel2SyncTestBucket(t)
			key := caching.NewCacheKey("key")
			old, err := newCacheValueEnvelope("old", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if err := bucket.memoryManager.Put(bucket.memoryBucketName, key, old); err != nil {
				t.Fatal(err)
			}
			writeErr, deleteErr, publishErr := errors.New("write failed"), errors.New("delete failed"), errors.New("publish failed")
			backend := &failingMemory{CacheBucket: bucket.memoryManager.GetBucket(bucket.memoryBucketName), writeErr: writeErr}
			if failDelete {
				backend.deleteErr = deleteErr
			}
			bucket.memoryManager = caching.NewCacheManager().AddBucket(bucket.memoryBucketName, backend)
			stored := false
			bucket.redisBucket = &testRedisBucket{put: func(_ CacheKey, data any, _ ...any) error {
				var value string
				if err := data.(cacheValueEnvelope).decode(&value); err != nil || value != "new" {
					t.Fatalf("L2 value %q %v", value, err)
				}
				stored = true
				return nil
			}}
			topic.err = publishErr
			err = bucket.Put(NewCacheKey("key"), "new")
			if !errors.Is(err, writeErr) || !errors.Is(err, publishErr) || (failDelete && !errors.Is(err, deleteErr)) {
				t.Fatalf("missing stage error: %v", err)
			}
			if !stored || backend.deletes != 1 || len(topic.events) != 1 {
				t.Fatal("expected L2 write, cleanup and publication")
			}
			if !failDelete {
				var value cacheValueEnvelope
				if err := bucket.memoryManager.Get(bucket.memoryBucketName, key, &value); !errors.Is(err, toolkitError.ErrCacheMiss) {
					t.Fatalf("old L1 retained: %v", err)
				}
			}
		})
	}
}

func TestLevel2RedisFailureStopsPutButNotEviction(t *testing.T) {
	topic := useTestTopics(t)
	bucket := newLevel2SyncTestBucket(t)
	backendErr := errors.New("redis failed")
	bucket.redisBucket = &testRedisBucket{put: func(CacheKey, any, ...any) error { return backendErr }, evict: func(CacheKey, ...any) error { return backendErr }}
	if err := bucket.Put(NewCacheKey("key"), "value"); !errors.Is(err, backendErr) {
		t.Fatal(err)
	}
	if len(topic.events) != 0 {
		t.Fatal("failed Redis write published")
	}
	if err := bucket.Evict(NewCacheKey("key")); !errors.Is(err, backendErr) {
		t.Fatal(err)
	}
	if len(topic.events) != 1 || topic.events[0].Operation != cacheSyncDelete {
		t.Fatal("failed Redis delete must still publish")
	}
}

func TestLevel2RebuildRejectsExpirationWhileWaiting(t *testing.T) {
	for _, conflicts := range []bool{false, true} {
		t.Run(fmt.Sprint(conflicts), func(t *testing.T) {
			bucket := newLevel2SyncTestBucket(t)
			key := NewCacheKey("key")
			shard := bucket.owner.syncShard(cacheSyncLockKey(bucket.bucketName, key.KeyFormat))
			ready := make(chan time.Time, 1)
			calls := 0
			bucket.redisBucket = &testRedisBucket{get: func(_ CacheKey, result any, _ ...any) error {
				calls++
				envelope, err := newCacheValueEnvelope("value", 50*time.Millisecond)
				if err != nil {
					return err
				}
				*result.(*cacheValueEnvelope) = envelope
				shard.Lock()
				if conflicts {
					shard.generation++
				}
				if !conflicts || calls == level2RebuildRetries {
					ready <- time.UnixMilli(envelope.ExpireAt)
				} else {
					shard.Unlock()
				}
				return nil
			}}
			done := make(chan error, 1)
			go func() { var result string; done <- bucket.Get(key, &result) }()
			select {
			case deadline := <-ready:
				timer := time.NewTimer(time.Until(deadline.Add(5 * time.Millisecond)))
				<-timer.C
				shard.Unlock()
			case <-time.After(time.Second):
				t.Fatal("rebuild did not reach lock wait")
			}
			select {
			case err := <-done:
				if !errors.Is(err, ErrCacheMiss) {
					t.Fatalf("expired snapshot returned: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("rebuild blocked")
			}
			var envelope cacheValueEnvelope
			if err := bucket.memoryManager.Get(bucket.memoryBucketName, caching.NewCacheKey("key"), &envelope); !errors.Is(err, toolkitError.ErrCacheMiss) {
				t.Fatalf("expired L1 fill: %v", err)
			}
		})
	}
}

func TestLevel2RebuildRetriesAndDoesNotPublish(t *testing.T) {
	topic := useTestTopics(t)
	bucket := newLevel2SyncTestBucket(t)
	calls := 0
	bucket.redisBucket = &testRedisBucket{get: func(_ CacheKey, result any, _ ...any) error {
		calls++
		value := "old"
		if calls == 1 {
			bucket.applySyncEvent(cacheSyncEvent{CacheKey: "key", Operation: cacheSyncDelete})
		} else {
			value = "new"
		}
		e, err := newCacheValueEnvelope(value, time.Minute)
		*result.(*cacheValueEnvelope) = e
		return err
	}}
	var got string
	if err := bucket.Get(NewCacheKey("key"), &got); err != nil || got != "new" || calls != 2 {
		t.Fatalf("value=%q calls=%d err=%v", got, calls, err)
	}
	if len(topic.events) != 0 {
		t.Fatal("L1 rebuild published a message")
	}
}
