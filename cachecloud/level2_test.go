package cachecloud

import (
	"errors"
	"testing"
	"time"

	"github.com/acexy/golang-toolkit/caching"
	toolkitError "github.com/acexy/golang-toolkit/error"
)

func newLevel2SyncTestBucket(t *testing.T) *level2CacheBucket {
	t.Helper()
	manager, err := newLevel2CacheManager("test", []BucketConfig{NewLevel2BucketConfig("level2", time.Minute, time.Minute)})
	if err != nil {
		t.Fatalf("create level2 manager: %v", err)
	}
	return manager.getBucket("level2")
}

func TestLevel2SyncAdvancesGenerationWithoutLocalValue(t *testing.T) {
	bucket := newLevel2SyncTestBucket(t)
	cacheKey := "model:1"
	shard := bucket.owner.syncShard(cacheSyncLockKey(bucket.bucketName, cacheKey))
	shard.Lock()
	before := shard.generation
	shard.Unlock()
	bucket.applySyncEvent(cacheSyncEvent{Operation: cacheSyncDelete, CacheKey: cacheKey})
	shard.Lock()
	after := shard.generation
	shard.Unlock()
	if after != before+1 {
		t.Fatalf("sync event must advance generation even without L1 value: before=%d after=%d", before, after)
	}
}

func TestLevel2SyncKeepsSameValueAndEvictsDifferentValue(t *testing.T) {
	bucket := newLevel2SyncTestBucket(t)
	key := caching.NewCacheKey("model:1")
	local, err := newCacheValueEnvelope("value", 10*time.Second)
	if err != nil {
		t.Fatalf("create envelope: %v", err)
	}
	if err := bucket.memoryManager.Put(bucket.memoryBucketName, key, local); err != nil {
		t.Fatalf("put envelope: %v", err)
	}
	bucket.applySyncEvent(cacheSyncEvent{Operation: cacheSyncPut, CacheKey: key.KeyFormat, ValueHash: local.ValueHash, ExpireAt: time.Now().Add(30 * time.Second).UnixMilli()})
	var refreshed cacheValueEnvelope
	if err := bucket.memoryManager.Get(bucket.memoryBucketName, key, &refreshed); err != nil {
		t.Fatalf("same value should remain cached: %v", err)
	}
	if refreshed.ExpireAt <= local.ExpireAt {
		t.Fatalf("same value should refresh expiration: before=%d after=%d", local.ExpireAt, refreshed.ExpireAt)
	}
	bucket.applySyncEvent(cacheSyncEvent{Operation: cacheSyncPut, CacheKey: key.KeyFormat, ValueHash: "different", ExpireAt: time.Now().Add(time.Minute).UnixMilli()})
	if err := bucket.memoryManager.Get(bucket.memoryBucketName, key, &refreshed); !errors.Is(err, toolkitError.ErrCacheMiss) {
		t.Fatalf("different value should evict L1, got %v", err)
	}
}
