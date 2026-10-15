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

// Get reads and decodes this bucket; backend failures are distinct from ErrCacheMiss.
func (m *redisCacheBucket) Get(key CacheKey, result any, keyArgs ...any) error {
	payload, err := redisstarter.StringCmd().GetBytes(redisstarter.NewRedisKey(m.keyPrefix+key.KeyFormat, m.expiration), keyArgs...)
	if errors.Is(err, redis.Nil) {
		return ErrCacheMiss
	}
	if err != nil {
		return err
	}
	return decodeCacheValue(payload, result)
}

// Put encodes and stores a value using the bucket expiration.
func (m *redisCacheBucket) Put(key CacheKey, data any, keyArgs ...any) error {
	payload, err := encodeCacheValue(data)
	if err != nil {
		return err
	}
	return redisstarter.StringCmd().SetBytes(redisstarter.NewRedisKey(m.keyPrefix+key.KeyFormat, m.expiration), payload, keyArgs...)
}

// Evict removes an entry; an absent entry succeeds.
func (m *redisCacheBucket) Evict(key CacheKey, keyArgs ...any) error {
	client := redisstarter.RawRedisClient()
	if client == nil {
		return redisstarter.ErrRedisClientNotStarted
	}
	return client.Del(context.Background(), m.keyPrefix+key.RawKeyString(keyArgs...)).Err()
}
