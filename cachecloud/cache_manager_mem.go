package cachecloud

import (
	"errors"
	"sync"

	"github.com/acexy/golang-toolkit/caching"
)

var memCache *memCacheManager

// memCacheManager 内存缓存管理器
type memCacheManager struct {
	manager *caching.CacheManager
	buckets map[string]*memeCacheBucket
	blocker sync.Mutex
}

func initMemCacheManager(configs ...CacheConfig) {
	if len(configs) > 0 {
		manager := caching.NewEmptyCacheBucketManager()
		for _, v := range configs {
			manager.AddBucket(string(v.bucketName), caching.NewSimpleBigCache(v.memExpire))
		}
		memCache = &memCacheManager{
			manager: manager,
			buckets: make(map[string]*memeCacheBucket),
		}
	}
}

func (m *memCacheManager) getBucket(bucketName BucketName) CacheBucket {
	name := string(bucketName)
	if bucket, ok := m.buckets[name]; ok {
		return bucket
	}
	if m.manager.GetBucket(name) == nil {
		return nil
	}
	defer m.blocker.Unlock()
	m.blocker.Lock()
	m.buckets[name] = &memeCacheBucket{
		bucket: m.manager.GetBucket(name),
	}
	return m.buckets[name]
}

// memeCacheBucket 内存缓存桶
type memeCacheBucket struct {
	bucket caching.CacheBucket
}

func (m *memeCacheBucket) Get(key CacheKey, result any, keyAppend ...interface{}) error {
	err := m.bucket.Get(caching.NewNemCacheKey(key.KeyFormat), result, keyAppend...)
	if errors.Is(err, caching.CacheMiss) {
		err = ErrCacheMiss
	}
	return err
}

func (m *memeCacheBucket) Put(key CacheKey, data any, keyAppend ...interface{}) error {
	return m.bucket.Put(caching.NewNemCacheKey(key.KeyFormat), data, keyAppend...)
}

func (m *memeCacheBucket) Evict(key CacheKey, keyAppend ...interface{}) error {
	return m.bucket.Evict(caching.NewNemCacheKey(key.KeyFormat), keyAppend...)
}
