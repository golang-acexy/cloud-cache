package cachecloud

import (
	"context"
	"encoding/json"
	"errors"
	"hash/fnv"
	"time"

	"github.com/acexy/golang-toolkit/caching"
	toolkitError "github.com/acexy/golang-toolkit/error"
	"github.com/acexy/golang-toolkit/logger"
	"github.com/golang-acexy/starter-redis/redisstarter"
	"github.com/redis/go-redis/v9"
)

const cacheSyncShardCount = 256

// cacheSyncTopic is the transport used by cache invalidation.
type cacheSyncTopic interface {
	// Publish sends metadata without a remote processing acknowledgment.
	Publish(redisstarter.RedisKey, string, ...any) error
	// SubscribeRetry delivers events until its context is canceled.
	SubscribeRetry(context.Context, redisstarter.RedisKey, func(*redis.Message))
}

type cacheSyncOperation string

const (
	cacheSyncPut    cacheSyncOperation = "put"
	cacheSyncDelete cacheSyncOperation = "delete"
)

type cacheSyncEvent struct {
	// NodeID identifies the originating process for self-event suppression.
	NodeID string `json:"nodeId"`
	// BucketName selects a bucket within the service topic.
	BucketName string `json:"bucketName"`
	// CacheKey is the resolved nonempty key, not a format string.
	CacheKey string `json:"cacheKey"`
	// Operation selects put metadata or delete invalidation.
	Operation cacheSyncOperation `json:"operation"`
	// ValueHash carries the put equality digest; delete events omit it.
	ValueHash string `json:"valueHash,omitempty"`
	// ExpireAt carries the positive put deadline in Unix milliseconds.
	ExpireAt int64 `json:"expireAt,omitempty"`
}

func newSyncEventPayload(bucketName, cacheKey string, operation cacheSyncOperation, valueHash string, expireAt int64) (string, error) {
	payload, err := json.Marshal(cacheSyncEvent{
		NodeID:     getNodeID(),
		BucketName: bucketName,
		CacheKey:   cacheKey,
		Operation:  operation,
		ValueHash:  valueHash,
		ExpireAt:   expireAt,
	})
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func handleSyncMessage(message *redis.Message, cacheType string, handler func(cacheSyncEvent)) {
	var event cacheSyncEvent
	if message == nil || json.Unmarshal([]byte(message.Payload), &event) != nil || !event.valid() {
		logger.Logrus().Warningln(ErrInvalidSyncEvent, cacheType)
		return
	}
	if event.NodeID == getNodeID() {
		return
	}

	handler(event)
}

func (e cacheSyncEvent) valid() bool {
	if e.NodeID == "" || e.BucketName == "" || e.CacheKey == "" {
		return false
	}
	switch e.Operation {
	case cacheSyncDelete:
		return true
	case cacheSyncPut:
		return e.ValueHash != "" && e.ExpireAt > 0
	default:
		return false
	}
}

func cacheSyncShardIndex(cacheKey string) uint32 {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(cacheKey))
	return hash.Sum32() % cacheSyncShardCount
}

func cacheSyncLockKey(bucketName, cacheKey string) string {
	return bucketName + ":" + cacheKey
}

// applyMemorySyncEvent requires the caller to hold its bucket/key mutation lock.
// L2 generation tracking remains with the caller, including missing entries.
func applyMemorySyncEvent(manager *caching.CacheManager, bucket caching.BucketName, bucketName string, expiration time.Duration, cacheType string, event cacheSyncEvent) {
	key := caching.NewCacheKey(event.CacheKey)
	if event.Operation == cacheSyncDelete {
		if err := evictMemory(manager, bucket, key); err == nil {
			logger.Logrus().Tracef("%s cache sync evicted key: bucket=%s key=%s operation=%s", cacheType, bucketName, event.CacheKey, event.Operation)
		} else {
			logger.Logrus().WithError(err).Warn("cache sync memory update failed")
		}
		return
	}

	var local cacheValueEnvelope
	if err := manager.Get(bucket, key, &local); err != nil {
		if !errors.Is(err, toolkitError.ErrCacheMiss) {
			logger.Logrus().WithError(err).Warn("cache sync memory read failed")
		}
		return
	}
	if local.ValueHash != event.ValueHash {
		if err := evictMemory(manager, bucket, key); err == nil {
			logger.Logrus().Tracef("%s cache sync evicted key: bucket=%s key=%s operation=%s reason=hash-mismatch", cacheType, bucketName, event.CacheKey, event.Operation)
		} else {
			logger.Logrus().WithError(err).Warn("cache sync memory update failed")
		}
		return
	}

	// Refresh equal-content deadlines without republishing to avoid event loops.
	if local.extendExpiration(event.ExpireAt, expiration, time.Now()) {
		if err := manager.Put(bucket, key, local); err == nil {
			logger.Logrus().Tracef("%s cache sync extended expiration: bucket=%s key=%s expireAt=%d", cacheType, bucketName, event.CacheKey, local.ExpireAt)
		} else {
			logger.Logrus().WithError(err).Warn("cache sync memory update failed")
		}
	}
}
