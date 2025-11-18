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
	"github.com/acexy/golang-toolkit/util/gob"
	"github.com/golang-acexy/starter-redis/redisstarter"
	"github.com/redis/go-redis/v9"
)

// 分布式内存缓存：内存缓存的同步只使用失效过期同步(及某个实例触发失效时，向其它实例同步实现信息清除该缓存)，并不保持持续同步。

var distMemCache *distMemCacheManager
var distMemTopicCmd = redisstarter.TopicCmd()
var distMemTopicName = "dis-mem-sync-topic"

// memCacheManager 内存缓存管理器
type distMemCacheManager struct {
	manager *caching.CacheManager
	buckets map[string]*distMemeCacheBucket
	blocker sync.Mutex
}

func initDistMemCacheManager(configs ...CacheConfig) {
	if len(configs) > 0 {
		manager := caching.NewEmptyCacheBucketManager()
		for _, v := range configs {
			manager.AddBucket(string(v.bucketName), caching.NewSimpleBigCache(v.memExpire))
		}
		if serviceNamePrefix != "" {
			distMemTopicName = serviceNamePrefix + ":" + distMemTopicName
		}
		distMemTopicCmd.SubscribeRetry(context.Background(), redisstarter.NewRedisKey(distMemTopicName), func(v *redis.Message) {
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
						logger.Logrus().Traceln("dist mem cache deleted", bucketName, cacheKey)
					}
					return
				}
				bytes, e := bucket.GetBytes(key)
				if e == nil {
					md5Bytes := hashing.Md5Bytes(bytes)
					currentSum := hex.EncodeToString(md5Bytes[:])
					if sum != currentSum {
						logger.Logrus().Traceln("dist mem cache changed", bucketName, cacheKey)
						_ = bucket.Evict(key)
					}
				}
			}
		})
		distMemCache = &distMemCacheManager{
			manager: manager,
			buckets: make(map[string]*distMemeCacheBucket),
		}
	}
}

func (m *distMemCacheManager) getBucket(bucketName BucketName) CacheBucket {
	name := string(bucketName)
	if bucket, ok := m.buckets[name]; ok {
		return bucket
	}
	if m.manager.GetBucket(name) == nil {
		return nil
	}
	defer m.blocker.Unlock()
	m.blocker.Lock()
	m.buckets[name] = &distMemeCacheBucket{
		bucket:     m.manager.GetBucket(name),
		bucketName: name,
	}
	return m.buckets[name]
}

// memeCacheBucket 内存缓存桶
type distMemeCacheBucket struct {
	bucket     caching.CacheBucket
	bucketName string
}

func (m *distMemeCacheBucket) publicEvent(bucketName, rawCacheKey, dataSum string) {
	err := distMemTopicCmd.Publish(redisstarter.NewRedisKey(distMemTopicName), getNodeId()+topicDelimiter+bucketName+topicDelimiter+rawCacheKey+topicDelimiter+dataSum)
	if err != nil {
		logger.Logrus().Warningln("event publish failed", rawCacheKey, err)
	}
}
func (m *distMemeCacheBucket) Get(key CacheKey, result any, keyAppend ...interface{}) error {
	err := m.bucket.Get(caching.NewNemCacheKey(key.KeyFormat), result, keyAppend...)
	if errors.Is(err, caching.CacheMiss) {
		err = ErrCacheMiss
	}
	return err
}

func (m *distMemeCacheBucket) Put(key CacheKey, data any, keyAppend ...interface{}) error {
	err := m.bucket.Put(caching.NewNemCacheKey(key.KeyFormat), data, keyAppend...)
	if err == nil {
		// 同步缓存数据发生变化的事件
		encode, _ := gob.Encode(data)
		md5Array := hashing.Md5Bytes(encode)
		m.publicEvent(m.bucketName, key.RawKeyString(keyAppend...), hex.EncodeToString(md5Array[:]))
	}
	return err
}

func (m *distMemeCacheBucket) Evict(key CacheKey, keyAppend ...interface{}) error {
	err := m.bucket.Evict(caching.NewNemCacheKey(key.KeyFormat), keyAppend...)
	if err != nil {
		// 同步缓存数据删除事件
		m.publicEvent(m.bucketName, key.RawKeyString(keyAppend...), "")
	}
	return err
}
