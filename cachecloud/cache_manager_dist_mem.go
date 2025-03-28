package cachecloud

import (
	"context"
	"github.com/acexy/golang-toolkit/caching"
	"github.com/acexy/golang-toolkit/logger"
	"github.com/golang-acexy/starter-redis/redisstarter"
	"sync"
)

var distMemCache *distMemCacheManager
var distMemTopicCmd = redisstarter.TopicCmd()

// memCacheManager 内存缓存管理器
type distMemCacheManager struct {
	manager *caching.CacheManager
	buckets map[string]CacheBucket
	blocker sync.Mutex
}

func initDistMemCacheManager(configs ...CacheConfig) {
	if len(configs) > 0 {
		manager := caching.NewEmptyCacheBucketManager()
		for _, v := range configs {
			manager.AddBucket(string(v.bucketName), caching.NewSimpleBigCache(v.expire))
		}
		topicName := "dis-mem-sync-topic"
		if serviceName != "" {
			topicName = serviceName + ":" + topicName
		}
		subscribe, err := distMemTopicCmd.Subscribe(context.Background(), redisstarter.NewRedisKey(topicName))
		if err != nil {
			logger.Logrus().Errorln("subscript", topicName, " failed", err)
		}
		go func() {
			for v := range subscribe {
				println(v)
			}
		}()
		memCache = &memCacheManager{
			manager: manager,
			buckets: make(map[string]CacheBucket),
		}
	}
}

func (m *distMemCacheManager) getBucket(bucketName BucketName) CacheBucket {
	name := string(bucketName)
	if bucket, ok := m.buckets[name]; ok {
		return bucket
	}
	defer m.blocker.Unlock()
	m.blocker.Lock()
	m.buckets[name] = distMemeCacheBucket{
		bucket:       m.manager.GetBucket(name),
		bucketPrefix: name,
	}
	return m.buckets[name]
}

// memeCacheBucket 内存缓存桶
type distMemeCacheBucket struct {
	bucket       caching.CacheBucket
	bucketPrefix string
}

func (m distMemeCacheBucket) Get(key CacheKey, result any, keyAppend ...interface{}) error {
	return m.bucket.Get(caching.NewNemCacheKey(key.KeyFormat), result, keyAppend...)
}

func (m distMemeCacheBucket) Put(key CacheKey, data any, keyAppend ...interface{}) error {
	return m.bucket.Put(caching.NewNemCacheKey(key.KeyFormat), data, keyAppend...)
}

func (m distMemeCacheBucket) Evict(key CacheKey, keyAppend ...interface{}) error {
	return m.bucket.Evict(caching.NewNemCacheKey(key.KeyFormat), keyAppend...)
}
