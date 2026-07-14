package cachecloud

import (
	"errors"

	"github.com/acexy/golang-toolkit/caching"
	toolkitError "github.com/acexy/golang-toolkit/error"
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

func (m *memoryCacheBucket) Get(key CacheKey, result any, keyArgs ...any) error {
	err := m.manager.Get(m.bucketName, caching.NewCacheKey(key.KeyFormat), result, keyArgs...)
	if errors.Is(err, toolkitError.ErrCacheMiss) {
		return ErrCacheMiss
	}
	return err
}

func (m *memoryCacheBucket) Put(key CacheKey, data any, keyArgs ...any) error {
	return m.manager.Put(m.bucketName, caching.NewCacheKey(key.KeyFormat), data, keyArgs...)
}

func (m *memoryCacheBucket) Evict(key CacheKey, keyArgs ...any) error {
	return m.manager.Evict(m.bucketName, caching.NewCacheKey(key.KeyFormat), keyArgs...)
}
