package cachecloud

import (
	"context"
	"errors"
	"time"

	"github.com/golang-acexy/starter-redis/redisstarter"
	"github.com/redis/go-redis/v9"
)

type redisCacheManager struct {
	buckets map[BucketName]*redisCacheBucket
}

func newRedisCacheManager(serviceName string, configs []BucketConfig) *redisCacheManager {
	if len(configs) == 0 {
		return nil
	}
	buckets := make(map[BucketName]*redisCacheBucket, len(configs))
	for _, config := range configs {
		buckets[config.bucketName] = &redisCacheBucket{
			keyPrefix:  serviceName + ":" + string(config.bucketName) + ":",
			expiration: config.redisExpiration,
		}
	}
	return &redisCacheManager{buckets: buckets}
}

func (m *redisCacheManager) getBucket(bucketName BucketName) *redisCacheBucket {
	return m.buckets[bucketName]
}

type redisCacheBucket struct {
	keyPrefix  string
	expiration time.Duration
}

func (m *redisCacheBucket) Get(key CacheKey, result any, keyArgs ...any) error {
	err := redisstarter.StringCmd().GetAnyWithGob(redisstarter.NewRedisKey(m.keyPrefix+key.KeyFormat, m.expiration), result, keyArgs...)
	if errors.Is(err, redis.Nil) {
		return ErrCacheMiss
	}
	return err
}

func (m *redisCacheBucket) Put(key CacheKey, data any, keyArgs ...any) error {
	return redisstarter.StringCmd().SetAnyWithGob(redisstarter.NewRedisKey(m.keyPrefix+key.KeyFormat, m.expiration), data, keyArgs...)
}

func (m *redisCacheBucket) Evict(key CacheKey, keyArgs ...any) error {
	client := redisstarter.RawRedisClient()
	if client == nil {
		return redisstarter.ErrRedisClientNotStarted
	}
	return client.Del(context.Background(), m.keyPrefix+key.RawKeyString(keyArgs...)).Err()
}
