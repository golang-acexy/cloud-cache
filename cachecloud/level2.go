package cachecloud

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/acexy/golang-toolkit/caching"
	toolkitError "github.com/acexy/golang-toolkit/error"
	"github.com/acexy/golang-toolkit/logger"
	"github.com/golang-acexy/starter-redis/redisstarter"
	"github.com/redis/go-redis/v9"
)

const (
	level2SyncTopicName  = "l2-mem-sync-topic"
	level2RebuildRetries = 3
)

var level2SyncCmd cacheSyncTopic = redisstarter.TopicCmd()

type level2CacheManager struct {
	buckets   map[BucketName]*level2CacheBucket
	syncTopic string
	manager   *caching.CacheManager
	shards    [cacheSyncShardCount]level2SyncShard
}

// level2SyncShard serializes L1 changes and tracks generations to reject stale fills.
type level2SyncShard struct {
	sync.Mutex
	generation uint64
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
			memoryManager:    memoryManager,
			memoryBucketName: managerBucketName,
			redisBucket: &redisCacheBucket{
				keyPrefix:  redisKeyPrefix + string(config.bucketName) + ":",
				expiration: config.redisExpiration,
			},
			redisExpiration:  config.redisExpiration,
			bucketName:       string(config.bucketName),
			syncTopic:        syncTopic,
			memoryExpiration: config.memoryExpiration,
		}
	}
	manager := &level2CacheManager{buckets: buckets, syncTopic: syncTopic, manager: memoryManager}
	for _, bucket := range buckets {
		bucket.owner = manager
	}
	return manager, nil
}

func (m *level2CacheManager) startSync() {
	level2SyncCmd.SubscribeRetry(context.Background(), redisstarter.NewRedisKey(m.syncTopic), func(message *redis.Message) {
		handleSyncMessage(message, "level2", m.handleSyncEvent)
	})
}

func (m *level2CacheManager) handleSyncEvent(event cacheSyncEvent) {
	bucket := m.getBucket(BucketName(event.BucketName))
	if bucket == nil {
		return
	}
	bucket.applySyncEvent(event)
}

func (m *level2CacheManager) syncShard(cacheKey string) *level2SyncShard {
	return &m.shards[cacheSyncShardIndex(cacheKey)]
}

func (m *level2CacheManager) getBucket(bucketName BucketName) *level2CacheBucket {
	return m.buckets[bucketName]
}

type level2CacheBucket struct {
	memoryManager    *caching.CacheManager
	memoryBucketName caching.BucketName
	redisBucket      CacheBucket
	redisExpiration  time.Duration
	bucketName       string
	syncTopic        string
	memoryExpiration time.Duration
	owner            *level2CacheManager
}

func (m *level2CacheBucket) publishEvent(cacheKey string, operation cacheSyncOperation, envelope cacheValueEnvelope) error {
	payload, err := newSyncEventPayload(m.bucketName, cacheKey, operation, envelope.ValueHash, envelope.ExpireAt)
	if err != nil {
		return err
	}
	return level2SyncCmd.Publish(redisstarter.NewRedisKey(m.syncTopic), payload)
}

// Get reads this bucket; empty resolved keys return ErrCacheKeyRequired, and backend failures remain distinct from ErrCacheMiss.
func (m *level2CacheBucket) Get(key CacheKey, result any, keyArgs ...any) error {
	rawKey := key.RawKeyString(keyArgs...)
	if rawKey == "" {
		return ErrCacheKeyRequired
	}
	cacheKey := caching.NewCacheKey(rawKey)
	shard := m.owner.syncShard(cacheSyncLockKey(m.bucketName, rawKey))
	shard.Lock()
	var envelope cacheValueEnvelope
	err := m.memoryManager.Get(m.memoryBucketName, cacheKey, &envelope)
	if err == nil {
		if envelope.expired(time.Now()) {
			_ = evictMemory(m.memoryManager, m.memoryBucketName, cacheKey)
		} else {
			shard.Unlock()
			return envelope.decode(result)
		}
	}
	shard.Unlock()
	if err != nil && !errors.Is(err, toolkitError.ErrCacheMiss) {
		return err
	}

	logger.Logrus().Traceln("memory cache missed", rawKey, "check redis")
	return m.rebuildFromRedis(NewCacheKey(rawKey), result)
}

// Put rejects empty resolved keys and stores a value; publication errors do not roll back stored data.
func (m *level2CacheBucket) Put(key CacheKey, data any, keyArgs ...any) error {
	rawKey := key.RawKeyString(keyArgs...)
	if rawKey == "" {
		return ErrCacheKeyRequired
	}
	redisEnvelope, err := newCacheValueEnvelope(data, m.redisExpiration)
	if err != nil {
		return err
	}
	shard := m.owner.syncShard(cacheSyncLockKey(m.bucketName, rawKey))
	shard.Lock()
	shard.generation++
	if err = m.redisBucket.Put(NewCacheKey(rawKey), redisEnvelope); err != nil {
		shard.Unlock()
		return err
	}
	memoryEnvelope := redisEnvelope
	memoryEnvelope.ExpireAt = min(redisEnvelope.ExpireAt, time.Now().Add(m.memoryExpiration).UnixMilli())
	cacheKey := caching.NewCacheKey(rawKey)
	memoryErr := m.memoryManager.Put(m.memoryBucketName, cacheKey, memoryEnvelope)
	if memoryErr != nil {
		// Clear an older local value while still holding the mutation lock.
		memoryErr = errors.Join(memoryErr, evictMemory(m.memoryManager, m.memoryBucketName, cacheKey))
	}
	shard.Unlock()
	// Publish after a successful Redis write even if the local L1 write failed.
	publishErr := m.publishEvent(rawKey, cacheSyncPut, memoryEnvelope)
	return errors.Join(memoryErr, publishErr)
}

// Evict removes the entry and publishes invalidation; absent entries succeed.
func (m *level2CacheBucket) Evict(key CacheKey, keyArgs ...any) error {
	rawKey := key.RawKeyString(keyArgs...)
	if rawKey == "" {
		return ErrCacheKeyRequired
	}
	shard := m.owner.syncShard(cacheSyncLockKey(m.bucketName, rawKey))
	shard.Lock()
	shard.generation++
	redisErr := m.redisBucket.Evict(NewCacheKey(rawKey))
	memoryErr := evictMemory(m.memoryManager, m.memoryBucketName, caching.NewCacheKey(rawKey))
	shard.Unlock()
	publishErr := m.publishEvent(rawKey, cacheSyncDelete, cacheValueEnvelope{})
	return errors.Join(redisErr, memoryErr, publishErr)
}

func (m *level2CacheBucket) rebuildFromRedis(key CacheKey, result any, keyArgs ...any) error {
	rawKey := key.RawKeyString(keyArgs...)
	shard := m.owner.syncShard(cacheSyncLockKey(m.bucketName, rawKey))
	var latest cacheValueEnvelope

	for range level2RebuildRetries {
		shard.Lock()
		generation := shard.generation
		shard.Unlock()

		if err := m.redisBucket.Get(key, &latest, keyArgs...); err != nil {
			return err
		}
		if latest.expired(time.Now()) {
			return ErrCacheMiss
		}

		latest.ExpireAt = min(latest.ExpireAt, time.Now().Add(m.memoryExpiration).UnixMilli())
		shard.Lock()
		if generation != shard.generation {
			shard.Unlock()
			continue
		}
		if latest.expired(time.Now()) {
			shard.Unlock()
			return ErrCacheMiss
		}
		err := m.memoryManager.Put(m.memoryBucketName, caching.NewCacheKey(rawKey), latest)
		shard.Unlock()
		if err != nil {
			logger.Logrus().Warningln("rebuild memory cache failed", err)
		}
		return latest.decode(result)
	}

	// Bound retries, but never intentionally return an already expired snapshot.
	if latest.expired(time.Now()) {
		return ErrCacheMiss
	}
	return latest.decode(result)
}

func (m *level2CacheBucket) applySyncEvent(event cacheSyncEvent) {
	shard := m.owner.syncShard(cacheSyncLockKey(m.bucketName, event.CacheKey))
	shard.Lock()
	defer shard.Unlock()
	shard.generation++

	applyMemorySyncEvent(m.memoryManager, m.memoryBucketName, m.bucketName, m.memoryExpiration, "level2 memory", event)
}
