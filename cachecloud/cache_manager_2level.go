package cachecloud

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"sync"

	"github.com/acexy/golang-toolkit/caching"
	"github.com/acexy/golang-toolkit/crypto/hashing"
	"github.com/acexy/golang-toolkit/logger"
	"github.com/acexy/golang-toolkit/util/coll"
	"github.com/acexy/golang-toolkit/util/gob"
	"github.com/golang-acexy/starter-redis/redisstarter"
	"github.com/redis/go-redis/v9"
)

// 二级缓存：内存缓存作为一级 redis缓存为二级，如果内存缓存中没有发现会查看redis，如果redis存在会重建内存缓存
// 由于重建的情况存在，所以内存缓存的过期时间在多个实例时可能不会一致，需要合理设计内存过期时间和redis的过期时间以及使用场景

var level2Cache *secondLevelCacheManager
var level2TopicCmd = redisstarter.TopicCmd()
var level2TopicName = "2l-mem-sync-topic"

// secondLevelCacheManager 二级缓存管理器
type secondLevelCacheManager struct {
	memCacheManager *caching.CacheManager

	configs            []CacheConfig
	baseRedisKeyPrefix string
	buckets            map[string]*secondLevelCacheBucket
	mutex              sync.Mutex
}

func initSecondLevelCacheManager(configs ...CacheConfig) {
	if len(configs) > 0 {
		manager := caching.NewEmptyCacheBucketManager()
		for _, v := range configs {
			manager.AddBucket(string(v.bucketName), caching.NewSimpleBigCache(v.memExpire))
		}
		if serviceNamePrefix != "" {
			level2TopicName = serviceNamePrefix + ":" + level2TopicName
		}
		level2TopicCmd.SubscribeRetry(context.Background(), redisstarter.NewRedisKey(level2TopicName), func(v *redis.Message) {
			if !strings.HasPrefix(v.Payload, getNodeId()) {
				split := strings.SplitN(v.Payload, topicDelimiter, 4)
				bucketName := split[1]
				cacheKey := split[2]
				sum := split[3]
				key := caching.NewNemCacheKey(cacheKey)
				bucket := manager.GetBucket(bucketName)
				if sum == "" {
					err := bucket.Evict(key)
					if err == nil {
						logger.Logrus().Traceln("l2 cache deleted", bucketName, cacheKey)
					}
					return
				}
				bytes, e := bucket.GetBytes(key)
				if e == nil {
					md5Bytes := hashing.Md5Bytes(bytes)
					currentSum := hex.EncodeToString(md5Bytes[:])
					if sum != currentSum {
						logger.Logrus().Traceln("l2 cache changed", bucketName, cacheKey)
						_ = bucket.Evict(key)
					}
				}
			}
		})
		var keyPrefix = "l2:"
		if serviceNamePrefix != "" {
			keyPrefix = serviceNamePrefix + ":" + keyPrefix
		}
		level2Cache = &secondLevelCacheManager{
			memCacheManager:    manager,
			configs:            configs,
			baseRedisKeyPrefix: keyPrefix,
			buckets:            make(map[string]*secondLevelCacheBucket),
		}
	}
}

func (s *secondLevelCacheManager) getBucket(bucketName BucketName) CacheBucket {
	name := string(bucketName)
	if bucket, ok := s.buckets[name]; ok {
		return bucket
	}
	defer s.mutex.Unlock()
	s.mutex.Lock()
	manager := s.memCacheManager.GetBucket(name)
	if manager == nil {
		return nil
	}
	config, _ := coll.SliceFilterFirstOne(s.configs, func(item CacheConfig) bool {
		return item.bucketName == bucketName
	})
	s.buckets[name] = &secondLevelCacheBucket{
		memBucket: manager,
		redisBucket: &redisCacheBucket{
			keyPrefix: s.baseRedisKeyPrefix + string(bucketName) + ":",
			expire:    config.redisExpire,
		},
		bucketName: string(bucketName),
	}
	return s.buckets[name]
}

// memeCacheBucket 内存缓存桶
type secondLevelCacheBucket struct {
	memBucket   caching.CacheBucket
	redisBucket *redisCacheBucket
	bucketName  string
}

func (m *secondLevelCacheBucket) publicEvent(bucketName, rawCacheKey, dataSum string) {
	err := level2TopicCmd.Publish(redisstarter.NewRedisKey(level2TopicName), getNodeId()+topicDelimiter+bucketName+topicDelimiter+rawCacheKey+topicDelimiter+dataSum)
	if err != nil {
		logger.Logrus().Warningln("event publish failed", rawCacheKey, err)
	}
}
func (m *secondLevelCacheBucket) Get(key CacheKey, result any, keyAppend ...interface{}) error {
	err := m.memBucket.Get(caching.NewNemCacheKey(key.KeyFormat), result, keyAppend...)
	if errors.Is(err, caching.CacheMiss) {
		logger.Logrus().Traceln("mem cache missed", key.RawKeyString(keyAppend...), "check redis")
		err = m.redisBucket.Get(key, result, keyAppend...)
		if errors.Is(err, ErrCacheMiss) {
			logger.Logrus().Traceln("redis cache missed", key.RawKeyString(keyAppend...))
		} else {
			logger.Logrus().Traceln("redis rebuild cache", key.RawKeyString(keyAppend...))
			_ = m.memBucket.Put(caching.NewNemCacheKey(key.KeyFormat), result, keyAppend...)
		}
	}
	return err
}

func (m *secondLevelCacheBucket) Put(key CacheKey, data any, keyAppend ...interface{}) error {
	_ = m.redisBucket.Put(key, data, keyAppend...)
	err := m.memBucket.Put(caching.NewNemCacheKey(key.KeyFormat), data, keyAppend...)
	if err == nil {
		// 同步缓存数据发生变化的事件
		encode, _ := gob.Encode(data)
		md5Bytes := hashing.Md5Bytes(encode)
		m.publicEvent(m.bucketName, key.RawKeyString(keyAppend...), hex.EncodeToString(md5Bytes[:]))
	}
	return err
}

func (m *secondLevelCacheBucket) Evict(key CacheKey, keyAppend ...interface{}) error {
	_ = m.redisBucket.Evict(key, keyAppend...)
	err := m.memBucket.Evict(caching.NewNemCacheKey(key.KeyFormat), keyAppend...)
	m.publicEvent(m.bucketName, key.RawKeyString(keyAppend...), "")
	return err
}
