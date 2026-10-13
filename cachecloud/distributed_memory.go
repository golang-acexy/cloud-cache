package cachecloud

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/acexy/golang-toolkit/caching"
	toolkitError "github.com/acexy/golang-toolkit/error"
	"github.com/golang-acexy/starter-redis/redisstarter"
	"github.com/redis/go-redis/v9"
)

const distMemSyncTopicName = "dis-mem-sync-topic"

var distMemSyncCmd cacheSyncTopic = redisstarter.TopicCmd()

type distMemCacheManager struct {
	buckets   map[BucketName]*distMemCacheBucket
	syncTopic string
	manager   *caching.CacheManager
	locks     [cacheSyncShardCount]sync.Mutex
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
			expiration:        config.memoryExpiration,
		}
	}
	distManager := &distMemCacheManager{buckets: buckets, syncTopic: syncTopic, manager: manager}
	for _, bucket := range buckets {
		bucket.owner = distManager
	}
	return distManager, nil
}

func (m *distMemCacheManager) startSync() {
	distMemSyncCmd.SubscribeRetry(context.Background(), redisstarter.NewRedisKey(m.syncTopic), func(message *redis.Message) {
		handleSyncMessage(message, "distributed memory", m.handleSyncEvent)
	})
}

func (m *distMemCacheManager) handleSyncEvent(event cacheSyncEvent) {
	bucket := m.getBucket(BucketName(event.BucketName))
	if bucket == nil {
		return
	}
	bucket.applySyncEvent(event)
}

func (m *distMemCacheManager) getBucket(bucketName BucketName) *distMemCacheBucket {
	return m.buckets[bucketName]
}

func (m *distMemCacheManager) syncLock(cacheKey string) *sync.Mutex {
	return &m.locks[cacheSyncShardIndex(cacheKey)]
}

type distMemCacheBucket struct {
	manager           *caching.CacheManager
	managerBucketName caching.BucketName
	bucketName        string
	syncTopic         string
	expiration        time.Duration
	owner             *distMemCacheManager
}

func (m *distMemCacheBucket) publishEvent(cacheKey string, operation cacheSyncOperation, envelope cacheValueEnvelope) error {
	payload, err := newSyncEventPayload(m.bucketName, cacheKey, operation, envelope.ValueHash, envelope.ExpireAt)
	if err != nil {
		return err
	}
	return distMemSyncCmd.Publish(redisstarter.NewRedisKey(m.syncTopic), payload)
}

// Get reads this bucket; empty resolved keys return ErrCacheKeyRequired, and backend failures remain distinct from ErrCacheMiss.
func (m *distMemCacheBucket) Get(key CacheKey, result any, keyArgs ...any) error {
	rawKey := key.RawKeyString(keyArgs...)
	if rawKey == "" {
		return ErrCacheKeyRequired
	}
	lock := m.owner.syncLock(cacheSyncLockKey(m.bucketName, rawKey))
	lock.Lock()
	var envelope cacheValueEnvelope
	err := m.manager.Get(m.managerBucketName, caching.NewCacheKey(rawKey), &envelope)
	if errors.Is(err, toolkitError.ErrCacheMiss) {
		lock.Unlock()
		return ErrCacheMiss
	}
	if err != nil {
		lock.Unlock()
		return err
	}
	if envelope.expired(time.Now()) {
		_ = evictMemory(m.manager, m.managerBucketName, caching.NewCacheKey(rawKey))
		lock.Unlock()
		return ErrCacheMiss
	}
	lock.Unlock()
	return envelope.decode(result)
}

// Put rejects empty resolved keys and stores a value; publication errors do not roll back stored data.
func (m *distMemCacheBucket) Put(key CacheKey, data any, keyArgs ...any) error {
	rawKey := key.RawKeyString(keyArgs...)
	if rawKey == "" {
		return ErrCacheKeyRequired
	}
	envelope, err := newCacheValueEnvelope(data, m.expiration)
	if err != nil {
		return err
	}
	cacheKey := caching.NewCacheKey(rawKey)
	lock := m.owner.syncLock(cacheSyncLockKey(m.bucketName, rawKey))
	lock.Lock()
	if err = m.manager.Put(m.managerBucketName, cacheKey, envelope); err != nil {
		lock.Unlock()
		return err
	}
	lock.Unlock()
	return m.publishEvent(rawKey, cacheSyncPut, envelope)
}

// Evict removes the entry and publishes invalidation; absent entries succeed.
func (m *distMemCacheBucket) Evict(key CacheKey, keyArgs ...any) error {
	rawKey := key.RawKeyString(keyArgs...)
	if rawKey == "" {
		return ErrCacheKeyRequired
	}
	lock := m.owner.syncLock(cacheSyncLockKey(m.bucketName, rawKey))
	lock.Lock()
	localErr := evictMemory(m.manager, m.managerBucketName, caching.NewCacheKey(rawKey))
	lock.Unlock()
	publishErr := m.publishEvent(rawKey, cacheSyncDelete, cacheValueEnvelope{})
	return errors.Join(localErr, publishErr)
}

func (m *distMemCacheBucket) applySyncEvent(event cacheSyncEvent) {
	lock := m.owner.syncLock(cacheSyncLockKey(m.bucketName, event.CacheKey))
	lock.Lock()
	defer lock.Unlock()

	applyMemorySyncEvent(m.manager, m.managerBucketName, m.bucketName, m.expiration, "distributed memory", event)
}
