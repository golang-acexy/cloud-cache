package cachecloud

import (
	"context"
	"errors"

	"github.com/acexy/golang-toolkit/caching"
	"github.com/acexy/golang-toolkit/crypto/hashing"
	toolkitError "github.com/acexy/golang-toolkit/error"
	"github.com/golang-acexy/starter-redis/redisstarter"
	"github.com/redis/go-redis/v9"
)

const distMemSyncTopicName = "dis-mem-sync-topic"

var distMemSyncCmd = redisstarter.TopicCmd()

type distMemCacheManager struct {
	buckets   map[BucketName]*distMemCacheBucket
	syncTopic string
	manager   *caching.CacheManager
}

func newDistMemCacheManager(serviceName string, configs []BucketConfig) (*distMemCacheManager, error) {
	if len(configs) == 0 {
		return nil, nil
	}
	manager := caching.NewCacheManager()
	buckets := make(map[BucketName]*distMemCacheBucket, len(configs))
	syncTopic := serviceName + ":" + distMemSyncTopicName
	for _, config := range configs {
		bucket, err := caching.NewSimpleBigCache(config.memoryExpiration)
		if err != nil {
			return nil, err
		}
		managerBucketName := caching.NewBucketName(string(config.bucketName))
		manager.AddBucket(managerBucketName, bucket)
		buckets[config.bucketName] = &distMemCacheBucket{
			manager:           manager,
			managerBucketName: managerBucketName,
			bucketName:        string(config.bucketName),
			syncTopic:         syncTopic,
		}
	}
	return &distMemCacheManager{buckets: buckets, syncTopic: syncTopic, manager: manager}, nil
}

func (m *distMemCacheManager) startSync() {
	distMemSyncCmd.SubscribeRetry(context.Background(), redisstarter.NewRedisKey(m.syncTopic), func(message *redis.Message) {
		handleSyncMessage(m.manager, message, "distributed memory")
	})
}

func (m *distMemCacheManager) getBucket(bucketName BucketName) *distMemCacheBucket {
	return m.buckets[bucketName]
}

type distMemCacheBucket struct {
	manager           *caching.CacheManager
	managerBucketName caching.BucketName
	bucketName        string
	syncTopic         string
}

func (m *distMemCacheBucket) publishEvent(cacheKey, dataHash string) error {
	payload, err := newSyncEventPayload(m.bucketName, cacheKey, dataHash)
	if err != nil {
		return err
	}
	return distMemSyncCmd.Publish(redisstarter.NewRedisKey(m.syncTopic), payload)
}

func (m *distMemCacheBucket) Get(key CacheKey, result any, keyArgs ...any) error {
	err := m.manager.Get(m.managerBucketName, caching.NewCacheKey(key.KeyFormat), result, keyArgs...)
	if errors.Is(err, toolkitError.ErrCacheMiss) {
		return ErrCacheMiss
	}
	return err
}

func (m *distMemCacheBucket) Put(key CacheKey, data any, keyArgs ...any) error {
	cacheKey := caching.NewCacheKey(key.KeyFormat)
	if err := m.manager.Put(m.managerBucketName, cacheKey, data, keyArgs...); err != nil {
		return err
	}
	dataBytes, err := m.manager.GetBytes(m.managerBucketName, cacheKey, keyArgs...)
	if err != nil {
		return err
	}
	return m.publishEvent(key.RawKeyString(keyArgs...), hashing.Md5Hex(string(dataBytes)))
}

func (m *distMemCacheBucket) Evict(key CacheKey, keyArgs ...any) error {
	localErr := m.manager.Evict(m.managerBucketName, caching.NewCacheKey(key.KeyFormat), keyArgs...)
	publishErr := m.publishEvent(key.RawKeyString(keyArgs...), "")
	return errors.Join(localErr, publishErr)
}
