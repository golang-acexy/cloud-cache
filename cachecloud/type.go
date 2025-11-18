package cachecloud

import (
	"errors"
	"fmt"
	"time"
)

const (
	BucketTypeMem     BucketType = "mem"
	BucketTypeDistMem            = "dist-mem"
	BucketTypeRedis              = "redis"
	BucketTypeLevel2             = "level-2"

	topicDelimiter = "<@.>"
)

var ErrCacheMiss = errors.New("cache miss")

type Option struct {
	ServiceName string // 服务名称 可用于防止隔离不同服务使用相同redis出现的key冲突
	// 是否允许自动开启二级缓存
	// 启用时：如果mem缓存类型与redis缓存类型的存储桶出现相同名，那么该存储桶将自动启用二级缓存管理机制
	// 如果检测到mem缓存存储桶被适配了二级缓存机制，原始定义的mem缓存类型的存储桶将自动放弃初始化
	AutoEnable2LevelCache bool
}

// BucketName 存储桶名称
type BucketName string

// BucketType 存储桶类型
type BucketType string

type Supplier[T any] func() (T, bool)

// CacheConfig 缓存key
type CacheConfig struct {
	bucketName  BucketName    // 存储桶名称
	memExpire   time.Duration // 内存过期时间
	redisExpire time.Duration // redis过期时间
	typ         BucketType    // 存储桶类型
}

type CacheKey struct {
	// 最终key值的格式化格式 将使用 fmt.Sprintf(key.KeyFormat, keyAppend) 进行处理
	KeyFormat string
}

// RawKeyString 返回原始的key字符串
func (c CacheKey) RawKeyString(keyAppend ...interface{}) string {
	if len(keyAppend) > 0 {
		return fmt.Sprintf(c.KeyFormat, keyAppend...)
	}
	return c.KeyFormat
}

type CacheBucket interface {
	// Get 获取指定key对应的值
	// result 值类型指针 缓存未命中时返回标准错误 ErrCacheMiss
	Get(key CacheKey, result any, keyAppend ...interface{}) error

	// Put 设置key对应值
	Put(key CacheKey, data any, keyAppend ...interface{}) error

	// Evict 清除缓存
	Evict(key CacheKey, keyAppend ...interface{}) error
}
