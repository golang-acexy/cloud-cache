package cachecloud

import "time"

const (
	BucketTypeMem     BucketType = "mem"
	BucketType2L                 = "2l"
	BucketTypeDistMem            = "dist-mem"
	BucketTypeRedis              = "redis"

	topicDelimiter = "<@>"
)

// BucketName 存储桶名称
type BucketName string
type BucketType string

// CacheConfig 缓存key
type CacheConfig struct {
	bucketName BucketName    // 存储桶名称
	expire     time.Duration // 过期时间
	typ        BucketType    // 存储桶类型
}

func NewCacheConfig(name BucketName, expire time.Duration, typ BucketType) CacheConfig {
	return CacheConfig{
		bucketName: name,
		expire:     expire,
		typ:        typ,
	}
}

type CacheKey struct {
	// 最终key值的格式化格式 将使用 fmt.Sprintf(key.KeyFormat, keyAppend) 进行处理
	KeyFormat string
}

type CacheBucket interface {
	// Get 获取指定key对应的值
	// result 值类型指针
	Get(key CacheKey, result any, keyAppend ...interface{}) error

	// Put 设置key对应值
	Put(key CacheKey, data any, keyAppend ...interface{}) error

	// Evict 清除缓存
	Evict(key CacheKey, keyAppend ...interface{}) error
}

type cacheManager interface {
	// GetBucket 获取存储桶
	getBucket(bucketName BucketName) CacheBucket
}
