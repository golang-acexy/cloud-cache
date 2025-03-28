package cachecloud

import "errors"

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
