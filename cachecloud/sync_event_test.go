package cachecloud

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/acexy/golang-toolkit/caching"
	"github.com/acexy/golang-toolkit/crypto/hashing"
	toolkitError "github.com/acexy/golang-toolkit/error"
	"github.com/redis/go-redis/v9"
)

func newSyncTestCache(t *testing.T) (*caching.CacheManager, caching.BucketName, caching.CacheKey) {
	t.Helper()
	bucket, err := caching.NewSimpleBigCache(time.Minute)
	if err != nil {
		t.Fatalf("create memory bucket: %v", err)
	}
	bucketName := caching.NewBucketName("sync-test")
	manager := caching.NewCacheManager().AddBucket(bucketName, bucket)
	return manager, bucketName, caching.NewCacheKey("model:1")
}

func syncMessage(t *testing.T, event cacheSyncEvent) *redis.Message {
	t.Helper()
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal sync event: %v", err)
	}
	return &redis.Message{Payload: string(payload)}
}

func TestHandleSyncMessageKeepsSameValue(t *testing.T) {
	manager, bucketName, key := newSyncTestCache(t)
	want := []byte("same-value")
	if err := manager.PutBytes(bucketName, key, want); err != nil {
		t.Fatalf("put bytes: %v", err)
	}

	handleSyncMessage(manager, syncMessage(t, cacheSyncEvent{
		NodeID:     "another-node",
		BucketName: string(bucketName),
		CacheKey:   key.KeyFormat,
		DataHash:   hashing.Md5Hex(string(want)),
	}), "test")

	got, err := manager.GetBytes(bucketName, key)
	if err != nil {
		t.Fatalf("same value should remain cached: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("unexpected cached bytes: got %q, want %q", got, want)
	}
}

func TestHandleSyncMessageEvictsDifferentValue(t *testing.T) {
	manager, bucketName, key := newSyncTestCache(t)
	if err := manager.PutBytes(bucketName, key, []byte("old-value")); err != nil {
		t.Fatalf("put bytes: %v", err)
	}

	handleSyncMessage(manager, syncMessage(t, cacheSyncEvent{
		NodeID:     "another-node",
		BucketName: string(bucketName),
		CacheKey:   key.KeyFormat,
		DataHash:   hashing.Md5Hex("new-value"),
	}), "test")

	if _, err := manager.GetBytes(bucketName, key); !errors.Is(err, toolkitError.ErrCacheMiss) {
		t.Fatalf("different value should be evicted, got %v", err)
	}
}

func TestHandleSyncMessageAlwaysHandlesDeleteEvent(t *testing.T) {
	manager, bucketName, key := newSyncTestCache(t)
	if err := manager.PutBytes(bucketName, key, []byte("value")); err != nil {
		t.Fatalf("put bytes: %v", err)
	}
	message := syncMessage(t, cacheSyncEvent{
		NodeID:     "another-node",
		BucketName: string(bucketName),
		CacheKey:   key.KeyFormat,
	})

	handleSyncMessage(manager, message, "test")
	if _, err := manager.GetBytes(bucketName, key); !errors.Is(err, toolkitError.ErrCacheMiss) {
		t.Fatalf("delete event should evict cached value, got %v", err)
	}
	// 重复删除不应触发 panic。
	handleSyncMessage(manager, message, "test")
}

func TestHandleSyncMessageIgnoresSameNode(t *testing.T) {
	manager, bucketName, key := newSyncTestCache(t)
	if err := manager.PutBytes(bucketName, key, []byte("value")); err != nil {
		t.Fatalf("put bytes: %v", err)
	}
	handleSyncMessage(manager, syncMessage(t, cacheSyncEvent{
		NodeID:     getNodeID(),
		BucketName: string(bucketName),
		CacheKey:   key.KeyFormat,
	}), "test")
	if _, err := manager.GetBytes(bucketName, key); err != nil {
		t.Fatalf("same-node event should be ignored: %v", err)
	}
}

func TestHandleSyncMessageRejectsInvalidInput(t *testing.T) {
	manager, _, _ := newSyncTestCache(t)
	for _, message := range []*redis.Message{
		nil,
		{Payload: "not-json"},
		syncMessage(t, cacheSyncEvent{NodeID: "another-node"}),
		syncMessage(t, cacheSyncEvent{NodeID: "another-node", BucketName: "unknown", CacheKey: "key"}),
	} {
		handleSyncMessage(manager, message, "test")
	}
}

func TestSyncEventSupportsDelimiterInCacheKey(t *testing.T) {
	payload, err := newSyncEventPayload("bucket", "key<@.>part", "hash")
	if err != nil {
		t.Fatalf("create sync payload: %v", err)
	}
	var event cacheSyncEvent
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		t.Fatalf("decode sync payload: %v", err)
	}
	if event.CacheKey != "key<@.>part" {
		t.Fatalf("cache key changed during sync encoding: %q", event.CacheKey)
	}
}
