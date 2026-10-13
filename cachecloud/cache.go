package cachecloud

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// Each concrete result type owns a group, avoiding ambiguous type-name keys.
var cacheFlightGroups sync.Map // map[reflect.Type]*singleflight.Group

// NewMemBucketConfig configures process-local storage; Init requires a positive duration. Expiration follows BigCache cleanup, not a strict read deadline.
func NewMemBucketConfig(name BucketName, expiration time.Duration) BucketConfig {
	return BucketConfig{
		bucketName:       name,
		memoryExpiration: expiration,
		bucketType:       BucketTypeMem,
	}
}

// NewDistMemBucketConfig configures local values with asynchronous Redis invalidation; Init requires a positive duration.
func NewDistMemBucketConfig(name BucketName, expiration time.Duration) BucketConfig {
	return BucketConfig{
		bucketName:       name,
		memoryExpiration: expiration,
		bucketType:       BucketTypeDistMem,
	}
}

// NewRedisBucketConfig configures Gob values with a positive Redis TTL, validated by Init.
func NewRedisBucketConfig(name BucketName, expiration time.Duration) BucketConfig {
	return BucketConfig{
		bucketName:      name,
		redisExpiration: expiration,
		bucketType:      BucketTypeRedis,
	}
}

// NewLevel2BucketConfig configures L1 memory and shared Redis L2; both durations must be positive.
// L1 entries are capped by the L2 envelope deadline; durations need not be ordered.
func NewLevel2BucketConfig(name BucketName, memoryExpiration, redisExpiration time.Duration) BucketConfig {
	return BucketConfig{
		bucketName:       name,
		memoryExpiration: memoryExpiration,
		redisExpiration:  redisExpiration,
		bucketType:       BucketTypeLevel2,
	}
}

// NewCacheKey stores an unchecked fmt.Sprintf format; use consistent arguments for every operation.
func NewCacheKey(format string) CacheKey {
	return CacheKey{KeyFormat: format}
}

// GetBucket returns a configured bucket, or nil before initialization or for unknown names.
func GetBucket(bucketName BucketName) CacheBucket {
	return getBucket(bucketName)
}

// GetBucketByType returns a bucket matching both name and mode, or nil when unavailable.
func GetBucketByType(bucketName BucketName, bucketType BucketType) CacheBucket {
	return getBucketByType(bucketName, bucketType)
}

// Get decodes a cached value into a caller-owned compatible pointer.
// Nil and typed-nil pointers return ErrResultRequired; other destination errors come from the codec.
// Misses return ErrCacheMiss; decoding errors may leave partial results.
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

// Put encodes and stores a value using the bucket TTL.
// Distributed writes may store the value before returning a publication or L1 error; no rollback is provided.
func Put(bucketName BucketName, cacheKey CacheKey, data any, keyAppend ...any) error {
	bucket, err := resolveBucket(bucketName)
	if err != nil {
		return err
	}
	return bucket.Put(cacheKey, data, keyAppend...)
}

// Evict attempts deletion; distributed modes also publish invalidation and may return joined errors.
// Missing entries succeed; synchronized buckets reject empty resolved keys.
func Evict(bucketName BucketName, cacheKey CacheKey, keyAppend ...any) error {
	bucket, err := resolveBucket(bucketName)
	if err != nil {
		return err
	}
	return bucket.Evict(cacheKey, keyAppend...)
}

// Cacheable loads only after ErrCacheMiss; hits and infrastructure/codec errors do not invoke the loader.
// A nil loader preserves the miss. A false result skips storage; loader errors are returned unchanged.
// A cache write error is returned even after the loader filled result successfully.
// Concurrent misses are not coalesced; an in-flight loader can overwrite a newer write or repopulate an evicted key.
func Cacheable[T any](bucketName BucketName, cacheKey CacheKey, result *T, loader Loader[T], keyAppend ...any) error {
	if result == nil {
		return ErrResultRequired
	}
	bucket, err := resolveBucket(bucketName)
	if err != nil {
		return err
	}
	err = bucket.Get(cacheKey, result, keyAppend...)
	if !errors.Is(err, ErrCacheMiss) || loader == nil {
		return err
	}
	cacheable, err := loader(result)
	if err != nil || !cacheable {
		return err
	}
	return bucket.Put(cacheKey, result, keyAppend...)
}

// CacheableSingleflight coalesces overlapping misses in this process by bucket, resolved key, and T.
// Hits bypass coordination; the leader rechecks storage before calling one loader with a fresh zero value.
// Loaders for the same key must be equivalent and must not recursively request the same flight.
// Results must support Gob even when the loader skips storage; each caller receives an independent decoded value.
// Loader and storage errors are shared; a write error still returns the loaded value when decoding succeeds.
// Loader failures do not copy partial values. Panics propagate to participants; Goexit terminates participants.
// A nil loader preserves a miss without joining a flight. Callers must use independent result pointers.
// This does not coordinate other processes or prevent stale loader writes after Put/Evict; waits cannot be canceled.
func CacheableSingleflight[T any](bucketName BucketName, cacheKey CacheKey, result *T, loader Loader[T], keyAppend ...any) error {
	if result == nil {
		return ErrResultRequired
	}
	bucket, err := resolveBucket(bucketName)
	if err != nil {
		return err
	}
	key := NewCacheKey(cacheKey.RawKeyString(keyAppend...))
	var cached T
	err = bucket.Get(key, &cached)
	if err == nil {
		*result = cached
		return nil
	}
	if !errors.Is(err, ErrCacheMiss) || loader == nil {
		return err
	}
	group := cacheFlightGroup(reflect.TypeFor[T]())
	flightKey := strconv.Itoa(len(bucketName)) + ":" + string(bucketName) + key.KeyFormat
	shared, flightErr, _ := group.Do(flightKey, func() (any, error) {
		var value T
		readErr := bucket.Get(key, &value)
		cacheable := false
		if errors.Is(readErr, ErrCacheMiss) {
			// A failed decode must not seed the loader's destination.
			var zero T
			value = zero
			cacheable, readErr = loader(&value)
		}
		if readErr != nil {
			return nil, readErr
		}
		var payload bytes.Buffer
		if err := gob.NewEncoder(&payload).Encode(&value); err != nil {
			return nil, err
		}
		// Validate the shared snapshot before any write, including custom codec failures.
		var decoded T
		if err := gob.NewDecoder(bytes.NewReader(payload.Bytes())).Decode(&decoded); err != nil {
			return nil, err
		}
		var writeErr error
		if cacheable {
			writeErr = bucket.Put(key, &value)
		}
		return payload.Bytes(), writeErr
	})
	payload, _ := shared.([]byte)
	if payload != nil {
		var value T
		if err := gob.NewDecoder(bytes.NewReader(payload)).Decode(&value); err != nil {
			return errors.Join(flightErr, err)
		}
		*result = value
	}
	return flightErr
}

func cacheFlightGroup(resultType reflect.Type) *singleflight.Group {
	if group, ok := cacheFlightGroups.Load(resultType); ok {
		return group.(*singleflight.Group)
	}
	group, _ := cacheFlightGroups.LoadOrStore(resultType, &singleflight.Group{})
	return group.(*singleflight.Group)
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
	return value.Kind() == reflect.Pointer && value.IsNil()
}
