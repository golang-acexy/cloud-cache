package cachecloud

import (
	"fmt"
	"strings"
	"sync"

	"github.com/acexy/golang-toolkit/util/str"
)

type cacheRuntime struct {
	memory  *memoryCacheManager
	distMem *distMemCacheManager
	level2  *level2CacheManager
	redis   *redisCacheManager
}

var runtimeState struct {
	sync.RWMutex
	instance *cacheRuntime
}

// Init validates configuration and publishes one process-wide runtime after constructing all buckets.
// Service and bucket names containing colon or percent characters return ErrInvalidCacheName.
// A subsequent valid call returns ErrAlreadyInitialized; invalid configuration is checked first.
// Start Redis first for Redis-backed modes; success does not verify Redis health or subscription readiness.
// There is no public shutdown, reset, or reconfiguration API; failed construction does not clean up all allocated resources.
func Init(options Options, bucketConfigs ...BucketConfig) error {
	if err := validateConfig(options, bucketConfigs); err != nil {
		return err
	}

	runtimeState.Lock()
	defer runtimeState.Unlock()
	if runtimeState.instance != nil {
		return ErrAlreadyInitialized
	}

	runtime := &cacheRuntime{}
	var err error

	if runtime.memory, err = newMemoryCacheManager(filterBucketConfigs(bucketConfigs, BucketTypeMem)); err != nil {
		return err
	}
	runtime.redis = newRedisCacheManager(options.ServiceName, filterBucketConfigs(bucketConfigs, BucketTypeRedis))
	if runtime.distMem, err = newDistMemCacheManager(options.ServiceName, filterBucketConfigs(bucketConfigs, BucketTypeDistMem)); err != nil {
		return err
	}
	if runtime.level2, err = newLevel2CacheManager(options.ServiceName, filterBucketConfigs(bucketConfigs, BucketTypeLevel2)); err != nil {
		return err
	}

	runtimeState.instance = runtime
	if runtime.distMem != nil {
		runtime.distMem.startSync()
	}
	if runtime.level2 != nil {
		runtime.level2.startSync()
	}
	return nil
}

func loadRuntime() *cacheRuntime {
	runtimeState.RLock()
	defer runtimeState.RUnlock()
	return runtimeState.instance
}

func validateConfig(options Options, bucketConfigs []BucketConfig) error {
	if !str.HasText(options.ServiceName) {
		return ErrServiceNameRequired
	}
	if strings.ContainsAny(options.ServiceName, ":%") {
		return ErrInvalidCacheName
	}
	if len(bucketConfigs) == 0 {
		return ErrBucketConfigRequired
	}

	bucketNames := make(map[BucketName]struct{}, len(bucketConfigs))
	for _, config := range bucketConfigs {
		if !str.HasText(string(config.bucketName)) {
			return ErrBucketNameRequired
		}
		if strings.ContainsAny(string(config.bucketName), ":%") {
			return fmt.Errorf("%w: %s", ErrInvalidCacheName, config.bucketName)
		}
		if _, exists := bucketNames[config.bucketName]; exists {
			return fmt.Errorf("%w: %s", ErrDuplicateBucket, config.bucketName)
		}
		bucketNames[config.bucketName] = struct{}{}

		switch config.bucketType {
		case BucketTypeMem, BucketTypeDistMem:
			if config.memoryExpiration <= 0 {
				return fmt.Errorf("%w: %s", ErrInvalidExpiration, config.bucketName)
			}
		case BucketTypeRedis:
			if config.redisExpiration <= 0 {
				return fmt.Errorf("%w: %s", ErrInvalidExpiration, config.bucketName)
			}
		case BucketTypeLevel2:
			if config.memoryExpiration <= 0 || config.redisExpiration <= 0 {
				return fmt.Errorf("%w: %s", ErrInvalidExpiration, config.bucketName)
			}
		default:
			return fmt.Errorf("%w: %s", ErrUnsupportedBucketType, config.bucketType)
		}
	}
	return nil
}

func filterBucketConfigs(configs []BucketConfig, bucketType BucketType) []BucketConfig {
	result := make([]BucketConfig, 0)
	for _, config := range configs {
		if config.bucketType == bucketType {
			result = append(result, config)
		}
	}
	return result
}
