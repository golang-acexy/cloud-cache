package cachecloud

import (
	"errors"

	"github.com/acexy/golang-toolkit/caching"
	toolkitError "github.com/acexy/golang-toolkit/error"
	"github.com/allegro/bigcache/v3"
)

type memoryCacheManager struct {
	buckets map[BucketName]*memoryCacheBucket
}

func newMemoryCacheManager(configs []BucketConfig) (*memoryCacheManager, error) {
	if len(configs) == 0 {
		return nil, nil
	}
	manager := caching.NewCacheManager()
	buckets := make(map[BucketName]*memoryCacheBucket, len(configs))
	for _, config := range configs {
		bucket, err := caching.NewSimpleBigCache(config.memoryExpiration)
		if err != nil {
			return nil, err
		}
		bucketName := caching.NewBucketName(string(config.bucketName))
		manager.AddBucket(bucketName, bucket)
		buckets[config.bucketName] = &memoryCacheBucket{
			manager:    manager,
			bucketName: bucketName,
		}
	}
	return &memoryCacheManager{buckets: buckets}, nil
}

func (m *memoryCacheManager) getBucket(bucketName BucketName) *memoryCacheBucket {
	return m.buckets[bucketName]
}

type memoryCacheBucket struct {
	manager    *caching.CacheManager
	bucketName caching.BucketName
}

// Get reads and decodes this bucket; backend failures are distinct from ErrCacheMiss.
func (m *memoryCacheBucket) Get(key CacheKey, result any, keyArgs ...any) error {
	payload, err := m.manager.GetBytes(m.bucketName, caching.NewCacheKey(key.KeyFormat), keyArgs...)
	if errors.Is(err, toolkitError.ErrCacheMiss) {
		return ErrCacheMiss
	}
	if err != nil {
		return err
	}
	return decodeCacheValue(payload, result)
}

// Put encodes and stores a value using the bucket expiration.
func (m *memoryCacheBucket) Put(key CacheKey, data any, keyArgs ...any) error {
	payload, err := encodeCacheValue(data)
	if err != nil {
		return err
	}
	return m.manager.PutBytes(m.bucketName, caching.NewCacheKey(key.KeyFormat), payload, keyArgs...)
}

// Evict removes an entry; an absent entry succeeds.
func (m *memoryCacheBucket) Evict(key CacheKey, keyArgs ...any) error {
	return evictMemory(m.manager, m.bucketName, caching.NewCacheKey(key.KeyFormat), keyArgs...)
}

func evictMemory(manager *caching.CacheManager, bucket caching.BucketName, key caching.CacheKey, args ...any) error {
	err := manager.Evict(bucket, key, args...)
	if errors.Is(err, bigcache.ErrEntryNotFound) {
		return nil
	}
	return err
}
