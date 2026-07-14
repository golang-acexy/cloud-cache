package cachecloud

import (
	"errors"
	"fmt"
	"reflect"
	"time"
)

// NewMemBucketConfig 创建一个内存缓存桶配置
func NewMemBucketConfig(name BucketName, expiration time.Duration) BucketConfig {
	return BucketConfig{
		bucketName:       name,
		memoryExpiration: expiration,
		bucketType:       BucketTypeMem,
	}
}

// NewDistMemBucketConfig 创建一个分布式内存缓存桶配置
func NewDistMemBucketConfig(name BucketName, expiration time.Duration) BucketConfig {
	return BucketConfig{
		bucketName:       name,
		memoryExpiration: expiration,
		bucketType:       BucketTypeDistMem,
	}
}

// NewRedisBucketConfig 创建一个 Redis 缓存桶配置
func NewRedisBucketConfig(name BucketName, expiration time.Duration) BucketConfig {
	return BucketConfig{
		bucketName:      name,
		redisExpiration: expiration,
		bucketType:      BucketTypeRedis,
	}
}

// NewLevel2BucketConfig 创建一个二级缓存桶配置
func NewLevel2BucketConfig(name BucketName, memoryExpiration, redisExpiration time.Duration) BucketConfig {
	return BucketConfig{
		bucketName:       name,
		memoryExpiration: memoryExpiration,
		redisExpiration:  redisExpiration,
		bucketType:       BucketTypeLevel2,
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
func GetBucketByType(bucketName BucketName, bucketType BucketType) CacheBucket {
	return getBucketByType(bucketName, bucketType)
}

// Get 通过指定的存储桶获取缓存值
func Get(bucketName BucketName, cacheKey CacheKey, result any, keyAppend ...any) error {
	if isNilResult(result) {
		return ErrResultRequired
	}
	bucket, err := resolveBucket(bucketName)
	if err != nil {
		return err
	}
	return bucket.Get(cacheKey, result, keyAppend...)
}

// Put 通过指定的存储桶设置缓存值
func Put(bucketName BucketName, cacheKey CacheKey, data any, keyAppend ...any) error {
	bucket, err := resolveBucket(bucketName)
	if err != nil {
		return err
	}
	return bucket.Put(cacheKey, data, keyAppend...)
}

// Evict 通过指定的存储桶删除缓存值
func Evict(bucketName BucketName, cacheKey CacheKey, keyAppend ...any) error {
	bucket, err := resolveBucket(bucketName)
	if err != nil {
		return err
	}
	return bucket.Evict(cacheKey, keyAppend...)
}

// Cacheable 通过指定的存储桶和缓存key，获取缓存值，如果缓存值不存在，则调用supplier获取值，并设置缓存值
func Cacheable[T any](bucketName BucketName, cacheKey CacheKey, result *T, supplier Supplier[*T], keyAppend ...any) error {
	if result == nil {
		return ErrResultRequired
	}
	bucket, err := resolveBucket(bucketName)
	if err != nil {
		return err
	}
	err = bucket.Get(cacheKey, result, keyAppend...)
	if errors.Is(err, ErrCacheMiss) {
		if supplier == nil {
			return err
		}
		value, supplierErr := supplier()
		if supplierErr != nil {
			return supplierErr
		}
		if value == nil {
			return ErrSupplierReturnedNil
		}
		*result = *value
		return bucket.Put(cacheKey, value, keyAppend...)
	}
	return err
}

func resolveBucket(bucketName BucketName) (CacheBucket, error) {
	runtime := loadRuntime()
	if runtime == nil {
		return nil, ErrNotInitialized
	}
	bucket := getBucketFromRuntime(runtime, bucketName)
	if bucket == nil {
		return nil, fmt.Errorf("%w: %s", ErrBucketNotFound, bucketName)
	}
	return bucket, nil
}

func isNilResult(result any) bool {
	if result == nil {
		return true
	}
	value := reflect.ValueOf(result)
	return value.Kind() == reflect.Ptr && value.IsNil()
}
