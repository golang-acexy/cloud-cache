package cachecloud

import (
	"encoding/json"
	"fmt"

	"github.com/acexy/golang-toolkit/caching"
	"github.com/acexy/golang-toolkit/crypto/hashing"
	"github.com/acexy/golang-toolkit/logger"
	"github.com/redis/go-redis/v9"
)

type cacheSyncEvent struct {
	NodeID     string `json:"nodeId"`
	BucketName string `json:"bucketName"`
	CacheKey   string `json:"cacheKey"`
	DataHash   string `json:"dataHash,omitempty"`
}

func newSyncEventPayload(bucketName, cacheKey, dataHash string) (string, error) {
	payload, err := json.Marshal(cacheSyncEvent{
		NodeID:     getNodeID(),
		BucketName: bucketName,
		CacheKey:   cacheKey,
		DataHash:   dataHash,
	})
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func handleSyncMessage(manager *caching.CacheManager, message *redis.Message, cacheType string) {
	var event cacheSyncEvent
	if message == nil || json.Unmarshal([]byte(message.Payload), &event) != nil || event.NodeID == "" || event.BucketName == "" || event.CacheKey == "" {
		logger.Logrus().Warningln(ErrInvalidSyncEvent, cacheType)
		return
	}
	if event.NodeID == getNodeID() {
		return
	}

	bucket := manager.GetBucket(caching.NewBucketName(event.BucketName))
	if bucket == nil {
		logger.Logrus().Warningln(fmt.Errorf("%w: %s", ErrBucketNotFound, event.BucketName))
		return
	}
	key := caching.NewCacheKey(event.CacheKey)
	if event.DataHash == "" {
		if err := bucket.Evict(key); err == nil {
			logger.Logrus().Traceln(cacheType, "cache deleted", event.BucketName, event.CacheKey)
		}
		return
	}

	data, err := bucket.GetBytes(key)
	if err != nil {
		return
	}
	if hashing.Md5Hex(string(data)) != event.DataHash {
		logger.Logrus().Traceln(cacheType, "cache changed", event.BucketName, event.CacheKey)
		_ = bucket.Evict(key)
	}
}
