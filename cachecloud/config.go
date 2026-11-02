package cachecloud

import (
	"fmt"
	"time"

	"github.com/acexy/golang-toolkit/caching"
	"github.com/allegro/bigcache/v3"
)

func copyBigCacheConfig(configs []bigcache.Config) *bigcache.Config {
	if len(configs) == 0 {
		return nil
	}
	config := configs[0]
	return &config
}

func newMemoryBucket(config BucketConfig) (*caching.BigCacheBucket, error) {
	if config.bigCacheConfig == nil {
		return caching.NewSimpleBigCache(config.memoryExpiration)
	}
	settings := *config.bigCacheConfig
	settings.LifeWindow = config.memoryExpiration
	return caching.NewBigCacheByConfig(settings)
}

// validateBigCacheConfig checks custom backend constraints before any bucket allocation.
func validateBigCacheConfig(config BucketConfig) error {
	c := config.bigCacheConfig
	if c == nil {
		return nil
	}
	switch {
	case c.Shards <= 0 || c.Shards&(c.Shards-1) != 0:
		return fmt.Errorf("%w: Shards must be a positive power of two", ErrInvalidBigCacheConfig)
	case c.MaxEntrySize < 0 || c.MaxEntriesInWindow < 0 || c.HardMaxCacheSize < 0:
		return fmt.Errorf("%w: size settings must not be negative", ErrInvalidBigCacheConfig)
	case c.CleanWindow > 0 && config.memoryExpiration < time.Second:
		return fmt.Errorf("%w: memory TTL must be at least one second when CleanWindow is positive", ErrInvalidBigCacheConfig)
	}
	return nil
}
