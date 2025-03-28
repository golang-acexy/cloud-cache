package cachecloud

import (
	"errors"
	"github.com/acexy/golang-toolkit/util/coll"
	"github.com/golang-acexy/starter-redis/redisstarter"
	"time"
)

var redisCache *redisCacheManager

// redisCacheManager redis缓存管理器
type redisCacheManager struct {
	buckets     map[BucketName]*redisCacheBucket
	cacheBucket *redisCacheBucket
}

func initRedisCacheManager(configs ...CacheConfig) {
	if len(configs) > 0 {
		redisCache = &redisCacheManager{
			buckets: make(map[BucketName]*redisCacheBucket),
		}
		var keyPrefix string
		if serviceNamePrefix != "" {
			keyPrefix = serviceNamePrefix + ":"
		}
		coll.SliceForeachAll(configs, func(config CacheConfig) {
			redisCache.buckets[config.bucketName] = &redisCacheBucket{
				keyPrefix: keyPrefix + string(config.bucketName) + ":",
				expire:    config.expire,
			}
		})
	}
}

func (m *redisCacheManager) getBucket(bucketName BucketName) CacheBucket {
	return m.buckets[bucketName]
}

// redisCacheBucket redis缓存桶
type redisCacheBucket struct {
	keyPrefix string
	expire    time.Duration
}

func (m *redisCacheBucket) Get(key CacheKey, result any, keyAppend ...interface{}) error {
	return redisstarter.StringCmd().GetAnyWithGob(redisstarter.NewRedisKey(m.keyPrefix+key.KeyFormat, m.expire), result, keyAppend...)
}

func (m *redisCacheBucket) Put(key CacheKey, data any, keyAppend ...interface{}) error {
	return redisstarter.StringCmd().SetAnyWithGob(redisstarter.NewRedisKey(m.keyPrefix+key.KeyFormat, m.expire), data, keyAppend...)
}

func (m *redisCacheBucket) Evict(key CacheKey, keyAppend ...interface{}) error {
	result := redisstarter.KeyCmd().Del(redisstarter.NewRedisKey(m.keyPrefix+key.KeyFormat, m.expire), keyAppend...)
	if result > 0 {
		return nil
	}
	return errors.New("no effect")
}
