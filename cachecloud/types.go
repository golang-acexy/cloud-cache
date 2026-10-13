package cachecloud

import (
	"fmt"
	"time"

	"github.com/acexy/golang-toolkit/caching"
)

const (
	// BucketTypeMem selects process-local BigCache storage.
	BucketTypeMem BucketType = "mem"
	// BucketTypeDistMem selects node-local values with Redis Pub/Sub invalidation.
	BucketTypeDistMem BucketType = "dist-mem"
	// BucketTypeRedis selects shared Redis Gob storage.
	BucketTypeRedis BucketType = "redis"
	// BucketTypeLevel2 selects local L1 memory backed by shared Redis L2.
	BucketTypeLevel2 BucketType = "level-2"
)

// Options sets the process-wide cache namespace.
type Options struct {
	// ServiceName prefixes Redis keys and topics; it is required and must not contain colon or percent characters.
	ServiceName string
}

// BucketName identifies a unique bucket; Init rejects blank names and colon or percent characters.
type BucketName caching.BucketName

// BucketType selects a storage mode.
type BucketType string

// Loader fills a caller-owned result on a miss; true permits caching and false returns the value without storage.
// Cacheable may invoke concurrent loaders independently; CacheableSingleflight coalesces same-key misses.\n// The supplied destination and partial-result error behavior depend on the calling API.
type Loader[T any] func(result *T) (cacheable bool, err error)

// BucketConfig holds a constructor-created mode and TTLs, validated by Init.
type BucketConfig struct {
	bucketName       BucketName
	memoryExpiration time.Duration
	redisExpiration  time.Duration
	bucketType       BucketType
}

// CacheKey holds a format string and does not validate arguments or uniqueness.
type CacheKey struct {

	// KeyFormat is used verbatim without arguments and formatted with fmt.Sprintf otherwise.
	KeyFormat string
}

// RawKeyString formats only when arguments are present; format mismatches become fmt diagnostics in the key.
func (c CacheKey) RawKeyString(keyAppend ...any) string {
	if len(keyAppend) > 0 {
		return fmt.Sprintf(c.KeyFormat, keyAppend...)
	}
	return c.KeyFormat
}

// CacheBucket provides concurrent cache operations; callers must not concurrently mutate input data or result destinations.
// Direct bucket access bypasses package-level nil-result validation; use compatible non-nil decoding pointers.
// Distributed-memory and level-2 buckets reject empty resolved keys with ErrCacheKeyRequired.
type CacheBucket interface {

	// Get decodes into a compatible pointer; misses return ErrCacheMiss and other errors are preserved.
	Get(key CacheKey, result any, keyAppend ...any) error

	// Put stores Gob-compatible data; an error may follow a successful local or Redis write.
	Put(key CacheKey, data any, keyAppend ...any) error

	// Evict deletes idempotently and, where applicable, publishes invalidation; success does not acknowledge remote application.
	Evict(key CacheKey, keyAppend ...any) error
}
