package cachecloud

import (
	"errors"
	"testing"
	"time"

	"github.com/acexy/golang-toolkit/caching"
	toolkitError "github.com/acexy/golang-toolkit/error"
)

func newDistMemSyncTestBucket(t *testing.T) *distMemCacheBucket {
	t.Helper()
	manager := caching.NewCacheManager()
	bucket, err := caching.NewSimpleBigCache(time.Minute)
	if err != nil {
		t.Fatalf("create memory bucket: %v", err)
	}
	bucketName := caching.NewBucketName("sync-test")
	manager.AddBucket(bucketName, bucket)
	distManager := &distMemCacheManager{manager: manager}
	distBucket := &distMemCacheBucket{manager: manager, managerBucketName: bucketName, bucketName: string(bucketName), expiration: time.Minute, owner: distManager}
	return distBucket
}

func TestDistMemSyncKeepsSameValueAndRefreshesExpiration(t *testing.T) {
	bucket := newDistMemSyncTestBucket(t)
	key := caching.NewCacheKey("model:1")
	local, err := newCacheValueEnvelope("value", 10*time.Second)
	if err != nil {
		t.Fatalf("create envelope: %v", err)
	}
	if err := bucket.manager.Put(bucket.managerBucketName, key, local); err != nil {
		t.Fatalf("put envelope: %v", err)
	}
	bucket.applySyncEvent(cacheSyncEvent{Operation: cacheSyncPut, CacheKey: key.KeyFormat, ValueHash: local.ValueHash, ExpireAt: time.Now().Add(30 * time.Second).UnixMilli()})
	var got cacheValueEnvelope
	if err := bucket.manager.Get(bucket.managerBucketName, key, &got); err != nil {
		t.Fatalf("get refreshed envelope: %v", err)
	}
	if got.ValueHash != local.ValueHash || got.ExpireAt <= local.ExpireAt {
		t.Fatalf("same value should remain and refresh expiration: before=%+v after=%+v", local, got)
	}
}

func TestDistMemSyncEvictsDifferentValueAndDelete(t *testing.T) {
	for _, operation := range []cacheSyncOperation{cacheSyncPut, cacheSyncDelete} {
		t.Run(string(operation), func(t *testing.T) {
			bucket := newDistMemSyncTestBucket(t)
			key := caching.NewCacheKey("model:1")
			local, err := newCacheValueEnvelope("old", time.Minute)
			if err != nil {
				t.Fatalf("create envelope: %v", err)
			}
			if err := bucket.manager.Put(bucket.managerBucketName, key, local); err != nil {
				t.Fatalf("put envelope: %v", err)
			}
			bucket.applySyncEvent(cacheSyncEvent{Operation: operation, CacheKey: key.KeyFormat, ValueHash: "different", ExpireAt: time.Now().Add(time.Minute).UnixMilli()})
			var got cacheValueEnvelope
			if err := bucket.manager.Get(bucket.managerBucketName, key, &got); !errors.Is(err, toolkitError.ErrCacheMiss) {
				t.Fatalf("cache should be evicted, got %v", err)
			}
		})
	}
}
