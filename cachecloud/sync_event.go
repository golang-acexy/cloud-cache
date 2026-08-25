package cachecloud

import (
	"encoding/json"
	"hash/fnv"

	"github.com/acexy/golang-toolkit/logger"
	"github.com/redis/go-redis/v9"
)

const cacheSyncShardCount = 256

type cacheSyncOperation string

const (
	cacheSyncPut    cacheSyncOperation = "put"
	cacheSyncDelete cacheSyncOperation = "delete"
)

type cacheSyncEvent struct {
	NodeID     string             `json:"nodeId"`
	BucketName string             `json:"bucketName"`
	CacheKey   string             `json:"cacheKey"`
	Operation  cacheSyncOperation `json:"operation"`
	ValueHash  string             `json:"valueHash,omitempty"`
	ExpireAt   int64              `json:"expireAt,omitempty"`
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
