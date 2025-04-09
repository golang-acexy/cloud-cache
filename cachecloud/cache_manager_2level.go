package cachecloud

import (
	"context"
	"encoding/hex"
	"errors"
	"github.com/acexy/golang-toolkit/caching"
	"github.com/acexy/golang-toolkit/crypto/hashing"
	"github.com/acexy/golang-toolkit/logger"
	"github.com/acexy/golang-toolkit/util/coll"
	"github.com/acexy/golang-toolkit/util/gob"
	"github.com/golang-acexy/starter-redis/redisstarter"
	"github.com/redis/go-redis/v9"
	"strings"
	"sync"
)

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
				level2Manager := level2Cache.buckets[bucketName]
				bucket := level2Manager.memBucket
				logger.Logrus().Traceln("收到二级缓存内存缓存变化事件", bucketName, cacheKey, sum)
				if sum == "" {
					err := bucket.Evict(key)
					if err != nil {
						logger.Logrus().Errorln("二级缓存内存缓存值删除失败", bucketName, cacheKey, err)
					} else {
						logger.Logrus().Traceln("二级缓存内存缓存值已删除", bucketName, cacheKey)
					}
					return
				}
				bytes, e := bucket.GetBytes(key)
				if e == nil {
					md5Bytes := hashing.Md5Bytes(bytes)
					currentSum := hex.EncodeToString(md5Bytes[:])
					if sum != currentSum {
						logger.Logrus().Traceln("二级缓存内存缓存值已变化", bucketName, cacheKey)
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
		s.buckets[name] = nil
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
		logger.Logrus().Errorln("发布二级内存缓存变化事件失败", rawCacheKey, err)
	}
}
func (m *secondLevelCacheBucket) Get(key CacheKey, result any, keyAppend ...interface{}) error {
	err := m.memBucket.Get(caching.NewNemCacheKey(key.KeyFormat), result, keyAppend...)
	if errors.Is(err, caching.CacheMiss) {
		logger.Logrus().Traceln("内存缓存中未命中", key.RawKeyString(keyAppend...), "向redis中请求")
		err = m.redisBucket.Get(key, result, keyAppend...)
		if errors.Is(err, CacheMiss) {
			logger.Logrus().Traceln("redis中未命", key.RawKeyString(keyAppend...))
		} else {
			logger.Logrus().Traceln("redis中已命中", key.RawKeyString(keyAppend...), "重建mem缓存")
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
