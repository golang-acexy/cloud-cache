package cachecloud

import (
	"fmt"
	"time"

	"github.com/acexy/golang-toolkit/caching"
)

const (
	BucketTypeMem     BucketType = "mem"
	BucketTypeDistMem BucketType = "dist-mem"
	BucketTypeRedis   BucketType = "redis"
	BucketTypeLevel2  BucketType = "level-2"

)

// Options 定义缓存模块的全局配置。
type Options struct {
	ServiceName string // 服务名称 可用于防止隔离不同服务使用相同redis出现的key冲突
}

// BucketName 存储桶名称
type BucketName caching.BucketName

// BucketType 存储桶类型
type BucketType string

// Loader 在缓存未命中将实际数据加载到 result。
// 返回值 cacheable 表示是否需要将加载结果写入缓存。
type Loader[T any] func(result *T) (cacheable bool, err error)

// BucketConfig 定义缓存桶类型和过期时间。
type BucketConfig struct {
	bucketName       BucketName
	memoryExpiration time.Duration
	redisExpiration  time.Duration
	bucketType       BucketType
}

// CacheKey 定义支持动态参数的缓存键格式。
type CacheKey struct {
	// 最终key值的格式化格式 将使用 fmt.Sprintf(key.KeyFormat, keyAppend) 进行处理
	KeyFormat string
}

// RawKeyString 返回原始的key字符串
func (c CacheKey) RawKeyString(keyAppend ...any) string {
	if len(keyAppend) > 0 {
		return fmt.Sprintf(c.KeyFormat, keyAppend...)
	}
	return c.KeyFormat
}

type CacheBucket interface {
	// Get 获取指定key对应的值
	// result 值类型指针 缓存未命中时返回标准错误 ErrCacheMiss
	Get(key CacheKey, result any, keyAppend ...any) error

	// Put 设置key对应值
	Put(key CacheKey, data any, keyAppend ...any) error

	// Evict 清除缓存
	Evict(key CacheKey, keyAppend ...any) error
}
