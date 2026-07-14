package cachecloud

import (
	"context"
	"errors"

	"github.com/acexy/golang-toolkit/caching"
	"github.com/acexy/golang-toolkit/crypto/hashing"
	toolkitError "github.com/acexy/golang-toolkit/error"
	"github.com/acexy/golang-toolkit/logger"
	"github.com/golang-acexy/starter-redis/redisstarter"
	"github.com/redis/go-redis/v9"
)

const level2SyncTopicName = "2l-mem-sync-topic"

var level2SyncCmd = redisstarter.TopicCmd()

type level2CacheManager struct {
	buckets   map[BucketName]*level2CacheBucket
	syncTopic string
	manager   *caching.CacheManager
}

func newLevel2CacheManager(serviceName string, configs []BucketConfig) (*level2CacheManager, error) {
	if len(configs) == 0 {
		return nil, nil
	}
	memoryManager := caching.NewCacheManager()
	buckets := make(map[BucketName]*level2CacheBucket, len(configs))
	syncTopic := serviceName + ":" + level2SyncTopicName
	redisKeyPrefix := serviceName + ":l2:"
	for _, config := range configs {
		bucket, err := caching.NewSimpleBigCache(config.memoryExpiration)
		if err != nil {
			return nil, err
		}
		managerBucketName := caching.NewBucketName(string(config.bucketName))
		memoryManager.AddBucket(managerBucketName, bucket)
		buckets[config.bucketName] = &level2CacheBucket{
			memoryManager:   memoryManager,
			memoryBucketName: managerBucketName,
			redisBucket: &redisCacheBucket{
				keyPrefix:  redisKeyPrefix + string(config.bucketName) + ":",
				expiration: config.redisExpiration,
			},
			bucketName: string(config.bucketName),
			syncTopic:  syncTopic,
		}
	}
	return &level2CacheManager{buckets: buckets, syncTopic: syncTopic, manager: memoryManager}, nil
}

func (m *level2CacheManager) startSync() {
	level2SyncCmd.SubscribeRetry(context.Background(), redisstarter.NewRedisKey(m.syncTopic), func(message *redis.Message) {
		handleSyncMessage(m.manager, message, "level2")
	})
}

func (m *level2CacheManager) getBucket(bucketName BucketName) *level2CacheBucket {
	return m.buckets[bucketName]
}

type level2CacheBucket struct {
	memoryManager    *caching.CacheManager
	memoryBucketName caching.BucketName
	redisBucket      *redisCacheBucket
	bucketName       string
	syncTopic        string
}

func (m *level2CacheBucket) publishEvent(cacheKey, dataHash string) error {
	payload, err := newSyncEventPayload(m.bucketName, cacheKey, dataHash)
	if err != nil {
		return err
	}
	return level2SyncCmd.Publish(redisstarter.NewRedisKey(m.syncTopic), payload)
}

func (m *level2CacheBucket) Get(key CacheKey, result any, keyArgs ...any) error {
	cacheKey := caching.NewCacheKey(key.KeyFormat)
	err := m.memoryManager.Get(m.memoryBucketName, cacheKey, result, keyArgs...)
	if err == nil {
		return nil
	}
	if !errors.Is(err, toolkitError.ErrCacheMiss) {
		return err
	}

	logger.Logrus().Traceln("memory cache missed", key.RawKeyString(keyArgs...), "check redis")
	if err = m.redisBucket.Get(key, result, keyArgs...); err != nil {
		return err
	}
	logger.Logrus().Traceln("redis rebuild memory cache", key.RawKeyString(keyArgs...))
	if err = m.memoryManager.Put(m.memoryBucketName, cacheKey, result, keyArgs...); err != nil {
		logger.Logrus().Warningln("rebuild memory cache failed", err)
	}
	return nil
}

func (m *level2CacheBucket) Put(key CacheKey, data any, keyArgs ...any) error {
	if err := m.redisBucket.Put(key, data, keyArgs...); err != nil {
		return err
	}
	cacheKey := caching.NewCacheKey(key.KeyFormat)
	if err := m.memoryManager.Put(m.memoryBucketName, cacheKey, data, keyArgs...); err != nil {
		return err
	}
	dataBytes, err := m.memoryManager.GetBytes(m.memoryBucketName, cacheKey, keyArgs...)
	if err != nil {
		return err
	}
	return m.publishEvent(key.RawKeyString(keyArgs...), hashing.Md5Hex(string(dataBytes)))
}

func (m *level2CacheBucket) Evict(key CacheKey, keyArgs ...any) error {
	redisErr := m.redisBucket.Evict(key, keyArgs...)
	memoryErr := m.memoryManager.Evict(m.memoryBucketName, caching.NewCacheKey(key.KeyFormat), keyArgs...)
	publishErr := m.publishEvent(key.RawKeyString(keyArgs...), "")
	return errors.Join(redisErr, memoryErr, publishErr)
}
