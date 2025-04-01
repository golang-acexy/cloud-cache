package cachecloud

import (
	"context"
	"encoding/hex"
	"errors"
	"github.com/acexy/golang-toolkit/caching"
	"github.com/acexy/golang-toolkit/crypto/hashing"
	"github.com/acexy/golang-toolkit/logger"
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
	manager *caching.CacheManager
	buckets map[string]*secondLevelCacheBucket
	blocker sync.Mutex
}

func initSecondLevelCacheManager(memConfigs []CacheConfig) {
	if len(memConfigs) > 0 {
		manager := caching.NewEmptyCacheBucketManager()
		for _, v := range memConfigs {
			manager.AddBucket(string(v.bucketName), caching.NewSimpleBigCache(v.expire))
		}
		if serviceNamePrefix != "" {
			level2TopicName = serviceNamePrefix + ":" + level2TopicName
		}
		subscribe, err := level2TopicCmd.Subscribe(context.Background(), redisstarter.NewRedisKey(level2TopicName))
		if err != nil {
			logger.Logrus().Errorln("初始化二级内存缓存同步订阅事件失败", level2TopicName, err)
		}
		go func() {
			for v := range subscribe {
				if !strings.HasPrefix(v.Payload, getNodeId()) {
					logger.Logrus().Traceln("分布式内存缓存消息同步数据", v.String())
					split := strings.SplitN(v.Payload, topicDelimiter, 4)
					bucketName := split[1]
					cacheKey := split[2]
					sum := split[3]
					key := caching.NewNemCacheKey(cacheKey)
					bucket := manager.GetBucket(bucketName)
					if sum == "" {
						logger.Logrus().Traceln("分布式缓存值已删除", bucketName, cacheKey)
						_ = bucket.Evict(key)
						return
					}
					bytes, e := bucket.GetBytes(key)
					if e == nil {
						md5Array := hashing.Md5Bytes(bytes)
						currentSum := hex.EncodeToString(md5Array[:])
						if sum != currentSum {
							logger.Logrus().Traceln("分布式缓存值已变化", bucketName, cacheKey)
							_ = bucket.Evict(key)
						}
					}
				}
			}
		}()
		level2Cache = &secondLevelCacheManager{
			manager: manager,
			buckets: make(map[string]*secondLevelCacheBucket),
		}
	}
}

func (s *secondLevelCacheManager) getBucket(bucketName BucketName) CacheBucket {
	name := string(bucketName)
	if bucket, ok := s.buckets[name]; ok {
		return bucket
	}
	defer s.blocker.Unlock()
	s.blocker.Lock()
	manager := s.manager.GetBucket(name)
	if manager == nil {
		s.buckets[name] = nil
		return nil
	}
	s.buckets[name] = &secondLevelCacheBucket{
		memBucket:   s.manager.GetBucket(name),
		redisBucket: redisCache.getBucket(bucketName).(*redisCacheBucket),
		bucketName:  string(bucketName),
	}
	return s.buckets[name]
}

// memeCacheBucket 内存缓存桶
type secondLevelCacheBucket struct {
	memBucket   caching.CacheBucket
	redisBucket *redisCacheBucket

	bucketName string
}

func (m *secondLevelCacheBucket) publicEvent(bucketName, rawCacheKey, dataSum string) {
	err := level2TopicCmd.Publish(redisstarter.NewRedisKey(level2TopicName), getNodeId()+topicDelimiter+bucketName+topicDelimiter+rawCacheKey+topicDelimiter+dataSum)
	if err != nil {
		logger.Logrus().Errorln("发布二级内存缓存变化事件失败", rawCacheKey, err)
	}
}
func (m *secondLevelCacheBucket) Get(key CacheKey, result any, keyAppend ...interface{}) error {
	err := m.memBucket.Get(caching.NewNemCacheKey(key.KeyFormat), result, keyAppend...)
	if errors.Is(err, caching.SourceNotFound) {
		logger.Logrus().Traceln("内存缓存中未命中", key.RawKeyString(keyAppend...), "向redis中请求", err)
		err = m.redisBucket.Get(key, result, keyAppend...)
		if errors.Is(err, redis.Nil) {
			logger.Logrus().Traceln("redis中未命", key.RawKeyString(keyAppend...))
			err = CacheMiss
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
		md5Array := hashing.Md5Bytes(encode)
		m.publicEvent(m.bucketName, key.RawKeyString(keyAppend...), hex.EncodeToString(md5Array[:]))
	}
	return err
}

func (m *secondLevelCacheBucket) Evict(key CacheKey, keyAppend ...interface{}) error {
	_ = m.redisBucket.Evict(key, keyAppend...)
	err := m.memBucket.Evict(caching.NewNemCacheKey(key.KeyFormat), keyAppend...)
	if err != nil {
		// 同步缓存数据删除事件
		m.publicEvent(m.bucketName, key.RawKeyString(keyAppend...), "")
	}
	return err
}
