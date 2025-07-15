package cachecloud

import (
	"errors"
	"github.com/acexy/golang-toolkit/logger"
	"time"
)

// NewMemCacheConfig 创建一个内存缓存配置
func NewMemCacheConfig(name BucketName, expire time.Duration) CacheConfig {
	return CacheConfig{
		bucketName: name,
		memExpire:  expire,
		typ:        BucketTypeMem,
	}
}

// NewDistMemCacheConfig 创建一个分布式内存缓存配置
func NewDistMemCacheConfig(name BucketName, expire time.Duration) CacheConfig {
	return CacheConfig{
		bucketName: name,
		memExpire:  expire,
		typ:        BucketTypeDistMem,
	}
}

// NewRedisCacheConfig 创建一个redis缓存配置
func NewRedisCacheConfig(name BucketName, expire time.Duration) CacheConfig {
	return CacheConfig{
		bucketName:  name,
		redisExpire: expire,
		typ:         BucketTypeRedis,
	}
}

// NewLevel2CacheConfig 创建一个二级缓存配置
func NewLevel2CacheConfig(name BucketName, memExpire, redisExpire time.Duration) CacheConfig {
	return CacheConfig{
		bucketName:  name,
		memExpire:   memExpire,
		redisExpire: redisExpire,
		typ:         BucketTypeLevel2,
	}
}

// NewCacheKey 创建一个缓存key
func NewCacheKey(format string) CacheKey {
	return CacheKey{KeyFormat: format}
}

// GetBucket 通过指定的存储桶，获取最佳匹配的存储桶实例
func GetBucket(bucketName BucketName) CacheBucket {
	return getBucket(bucketName)
}

// GetBucketByType 通过指定的存储桶和类型，获取存储桶实例
func GetBucketByType(bucketName BucketName, typ BucketType) CacheBucket {
	return getBucketByType(bucketName, typ)
}

// GetCacheValue 通过指定的存储桶和缓存key，获取缓存值
func GetCacheValue(bucketName BucketName, cacheKey CacheKey, result any, keyAppend ...interface{}) error {
	bucket := getBucket(bucketName)
	if bucket == nil {
		return errors.New("bucket not found")
	}
	return bucket.Get(cacheKey, result, keyAppend...)
}

// PutCacheValue 通过指定的存储桶和缓存key，设置缓存值
func PutCacheValue(bucketName BucketName, cacheKey CacheKey, data any, keyAppend ...interface{}) error {
	bucket := getBucket(bucketName)
	if bucket == nil {
		return errors.New("bucket not found")
	}
	return bucket.Put(cacheKey, data, keyAppend...)
}

// EvictCache 通过指定的存储桶和缓存key，删除缓存值
func EvictCache(bucketName BucketName, cacheKey CacheKey, keyAppend ...interface{}) error {
	bucket := getBucket(bucketName)
	if bucket == nil {
		return errors.New("bucket not found")
	}
	return bucket.Evict(cacheKey, keyAppend...)
}

// Cacheable 通过指定的存储桶和缓存key，获取缓存值，如果缓存值不存在，则调用supplier获取值，并设置缓存值
func Cacheable[T any](bucketName BucketName, cacheKey CacheKey, result *T, supplier Supplier[*T], keyAppend ...interface{}) error {
	bucket := getBucket(bucketName)
	if bucket == nil {
		return errors.New("bucket not found")
	}
	err := bucket.Get(cacheKey, result, keyAppend...)
	if errors.Is(err, CacheMiss) {
		if supplier != nil {
			value, flag := supplier()
			if flag {
				*result = *value
				return bucket.Put(cacheKey, value, keyAppend...)
			} else {
				logger.Logrus().Traceln("rebuild cache failed, supplier get nil data")
			}
		}
	}
	return err
}
